package engine

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/stratum"
)

// In fixed mode the payout address comes from the config/env or, when the web
// UI has an admin password, from the Settings page. An address saved from the
// UI is stored in <data_dir>/payout-<coin>.json and wins over the config.
// Until an address is set, miners are refused at mining.authorize, so no work
// is ever issued without a verified payout script.

var errNoPayout = errors.New("no payout address set yet: set it in the web UI (The Ledger, Settings)")

// nodeUnreachable marks a payout check that failed because the node did not
// answer (transport or RPC error), not because it judged the address.
type nodeUnreachable struct{ err error }

func (e nodeUnreachable) Error() string { return e.err.Error() }
func (e nodeUnreachable) Unwrap() error { return e.err }

// A saved address whose check fails only because the node did not answer is
// retried this long at start; after that the engine exits so its restart
// policy tries again, rather than running with no payout.
var (
	payoutRetryFor   = 2 * time.Minute
	payoutRetryEvery = 2 * time.Second
)

// validateWithRetry checks addr, retrying while the node does not answer.
func (e *Engine) validateWithRetry(ctx context.Context, addr string) (*stratum.Payout, error) {
	deadline := time.Now().Add(payoutRetryFor)
	for i := 0; ; i++ {
		po, err := e.ValidatePayout(ctx, addr)
		if err == nil || !errors.As(err, new(nodeUnreachable)) || time.Now().After(deadline) {
			return po, err
		}
		if i%10 == 0 {
			e.log.Warn("node did not answer the payout address check; retrying", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(payoutRetryEvery):
		}
	}
}

func (e *Engine) payoutPath() string {
	if e.cfg.DataDir == "" {
		return ""
	}
	return filepath.Join(e.cfg.DataDir, "payout-"+e.cfg.Coin+".json")
}

func (e *Engine) fixedPayout() *stratum.Payout {
	e.payoutMu.RLock()
	defer e.payoutMu.RUnlock()
	return e.fixed
}

// initPayout loads the saved or configured address and verifies it with the
// node. A configured address that fails verification is fatal; a missing one
// is allowed (the config check guarantees the UI can set it).
func (e *Engine) initPayout(ctx context.Context) error {
	if p := e.payoutPath(); p != "" {
		var saved struct {
			Address string `json:"address"`
		}
		b, err := os.ReadFile(p)
		if err == nil {
			err = json.Unmarshal(b, &saved)
		}
		if err == nil && saved.Address != "" {
			po, verr := e.validateWithRetry(ctx, saved.Address)
			if errors.As(verr, new(nodeUnreachable)) {
				return fmt.Errorf("saved payout address %q could not be checked with the node: %w", saved.Address, verr)
			}
			if verr == nil {
				e.setFixed(po)
				e.log.Info("payout address verified (saved from the UI)", "address", po.Address, "script", hex.EncodeToString(po.Script))
				return nil
			} else {
				err = verr
			}
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			e.log.Error("ignoring saved payout address", "path", p, "err", err)
		}
	}
	if strings.TrimSpace(e.cfg.Payout.Address) == "" {
		e.log.Warn("no payout address set: miners are refused until one is set in the web UI (The Ledger, Settings)")
		return nil
	}
	po, err := e.validateWithRetry(ctx, e.cfg.Payout.Address)
	if err != nil {
		return fmt.Errorf("payout address: %w", err)
	}
	e.setFixed(po)
	e.log.Info("payout address verified", "address", po.Address, "script", hex.EncodeToString(po.Script))
	return nil
}

func (e *Engine) setFixed(p *stratum.Payout) {
	e.payoutMu.Lock()
	e.fixed = p
	e.payoutMu.Unlock()
}

// Payout implements ui.SettingsBackend: the current fixed payout address and
// whether it can be set from the UI.
func (e *Engine) Payout() (string, bool) {
	if e.cfg.Payout.Mode != "fixed" {
		return "", false
	}
	if p := e.fixedPayout(); p != nil {
		return p.Address, true
	}
	return "", true
}

// SetPayout implements ui.SettingsBackend: it verifies addr locally and with
// the node, saves it, and reconnects all miners so every new job pays it.
func (e *Engine) SetPayout(ctx context.Context, addr string) (string, error) {
	if e.cfg.Payout.Mode != "fixed" {
		return "", errors.New("payout mode is miner: each miner's username is its payout address")
	}
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", errors.New("payout address is empty")
	}
	po, err := e.ValidatePayout(ctx, addr)
	if err != nil {
		return "", err
	}
	if p := e.payoutPath(); p != "" {
		b, _ := json.MarshalIndent(map[string]string{"address": po.Address}, "", "  ")
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			return "", fmt.Errorf("cannot save payout address: %w", err)
		}
		if err := os.Rename(tmp, p); err != nil {
			return "", fmt.Errorf("cannot save payout address: %w", err)
		}
	}
	old := e.fixedPayout()
	e.setFixed(po)
	e.log.Info("payout address set from the UI", "address", po.Address, "script", hex.EncodeToString(po.Script))
	if old == nil || old.Address != po.Address {
		// Sessions keep the payout they authorized with (or were refused for
		// lack of one); reconnect them so they pick up the new address.
		e.srv.DropAll("payout address changed")
	}
	return po.Address, nil
}
