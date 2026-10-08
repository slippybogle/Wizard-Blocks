package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/slippybogle/wizard-blocks/internal/stratum"
)

// Live difficulty settings edited in the UI are saved to
// <data_dir>/settings-<coin>.json and override the config/env values on the
// next start. "Reset" deletes the file and restores the config values.

func (e *Engine) settingsPath() string {
	if e.cfg.DataDir == "" {
		return ""
	}
	return filepath.Join(e.cfg.DataDir, "settings-"+e.cfg.Coin+".json")
}

func (e *Engine) loadSavedSettings() {
	p := e.settingsPath()
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var d stratum.DiffSettings
	if err == nil {
		err = json.Unmarshal(b, &d)
	}
	if err == nil {
		err = e.srv.SetDiffSettings(d)
	}
	if err != nil {
		e.log.Warn("ignoring saved difficulty settings", "path", p, "err", err)
		return
	}
	e.log.Info("using difficulty settings saved from the UI (they override config/env)", "path", p)
}

// DiffSettings implements ui.SettingsBackend.
func (e *Engine) DiffSettings() (stratum.DiffSettings, bool, bool) {
	p := e.settingsPath()
	saved := false
	if p != "" {
		_, err := os.Stat(p)
		saved = err == nil
	}
	return e.srv.DiffSettings(), saved, p != ""
}

// UpdateDiffSettings validates, applies live and persists new settings.
func (e *Engine) UpdateDiffSettings(d stratum.DiffSettings) error {
	e.settingsMu.Lock()
	defer e.settingsMu.Unlock()
	if err := e.srv.SetDiffSettings(d); err != nil {
		return err
	}
	p := e.settingsPath()
	if p == "" {
		return nil // applied, but there is nowhere to persist it
	}
	b, err := json.MarshalIndent(e.srv.DiffSettings(), "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("applied but not saved: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("applied but not saved: %w", err)
	}
	return nil
}

// ResetDiffSettings restores the config/env settings and deletes the file.
func (e *Engine) ResetDiffSettings() error {
	e.settingsMu.Lock()
	defer e.settingsMu.Unlock()
	if p := e.settingsPath(); p != "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return e.srv.SetDiffSettings(e.baseDiff)
}
