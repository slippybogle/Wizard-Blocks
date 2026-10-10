package nodestatus

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Pruning: each node reads its prune target (MiB) from a small config file
// (-conf=/conf/<coin>.conf) that only this service writes. Changing it
// rewrites the file and stops the node over RPC; its container restarts
// (restart: unless-stopped) with the new target. Pruning cannot be turned
// off here: going back to a full node needs the whole chain downloaded again.

// PruneConfig is a node's pruning setup.
type PruneConfig struct {
	ConfFile string // e.g. /conf/litecoin.conf
	Default  int    // MiB, written when the file is missing or has no valid prune line
	Min      int    // the node's minimum (Litecoin 550, Dogecoin 2200)
	Options  []int  // MiB choices offered on the page
}

// ErrBadPrune is returned for a target that is not one of the options.
var ErrBadPrune = errors.New("not one of the prune options")

// readPrune returns the prune target in the conf file (0 if none).
func readPrune(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	v := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if k, val, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "prune" {
			if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
				v = n
			}
		}
	}
	return v, sc.Err()
}

// writePrune replaces the conf file atomically with one prune line.
func writePrune(path string, mib int) error {
	body := fmt.Sprintf("# Written by the Litecoin + Dogecoin Node page: change it there.\n# Prune target in MiB (the node restarts to apply it).\nprune=%d\n", mib)
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// EnsurePruneConf makes sure every node's conf file has a valid prune line
// (a node without one runs unpruned and fills the disk) that matches the
// last choice made on the page (an app update may reset the file). Nodes
// whose file it rewrote are restarted once they answer RPC. It returns
// their keys.
func (p *Poller) EnsurePruneConf() ([]string, error) {
	var fixed []string
	var errs []error
	for _, n := range p.nodes {
		pc := n.Prune
		if pc == nil {
			continue
		}
		p.mu.Lock()
		saved := p.prune[n.Key]
		p.mu.Unlock()
		v, err := readPrune(pc.ConfFile)
		if err == nil && v >= pc.Min && (saved == 0 || v == saved) {
			continue
		}
		want := pc.Default
		if saved >= pc.Min {
			want = saved // the last choice made on the page
		}
		if werr := writePrune(pc.ConfFile, want); werr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n.Name, werr))
			continue
		}
		fixed = append(fixed, n.Key)
		p.mu.Lock()
		p.restart[n.Key] = true
		p.mu.Unlock()
	}
	return fixed, errors.Join(errs...)
}

// SetPrune sets a node's prune target and restarts the node to apply it.
func (p *Poller) SetPrune(ctx context.Context, key string, mib int) error {
	n := p.node(key)
	if n == nil {
		return ErrUnknownNode
	}
	if n.Prune == nil {
		return ErrNotPrunable
	}
	if !slices.Contains(n.Prune.Options, mib) || mib < n.Prune.Min {
		return fmt.Errorf("%w: %d MiB (choose one of %v)", ErrBadPrune, mib, n.Prune.Options)
	}
	if err := writePrune(n.Prune.ConfFile, mib); err != nil {
		return fmt.Errorf("cannot save the prune target: %w", err)
	}
	p.mu.Lock()
	p.prune[key] = mib
	err := p.saveLocked()
	p.mu.Unlock()
	if err != nil {
		return fmt.Errorf("cannot save the prune target: %w", err)
	}
	if err := n.RPC.Call(ctx, "stop", nil, nil); err != nil {
		return fmt.Errorf("saved; restart the node to apply it (stop failed: %w)", err)
	}
	return nil
}

// ErrNotPrunable is returned for a node without a prune config.
var ErrNotPrunable = errors.New("this node's pruning is not set here")
