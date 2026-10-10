package stratum

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Limits for difficulty settings. The floor is low enough for regtest
// (network difficulty ~4.7e-10); the ceiling far exceeds any single miner.
const (
	DiffFloor          = 1e-12
	DiffCeiling        = 1e15
	MinTargetSeconds   = 1
	MaxTargetSeconds   = 600
	maxOverrides       = 1000
	maxWorkerNameBytes = 256
)

// DiffSettings are the difficulty settings that can change at runtime
// (from the config/env at startup, or live from the UI).
//
// Precedence for a connection, highest first:
//  1. an override for one of its workers: an exact name, else the longest
//     matching prefix pattern ("rental.*"), clamped to [Min, Max]
//  2. the miner's password "d=<diff>" (unless IgnorePasswordDiff), clamped
//  3. FixedDiff, if > 0 (disables vardiff for everyone else)
//  4. vardiff between Min and Max, aiming at one share per TargetSeconds,
//     starting from mining.suggest_difficulty (unless IgnoreSuggest) or
//     else from Start
type DiffSettings struct {
	Min           float64            `json:"vardiff_min"`
	Max           float64            `json:"vardiff_max"`
	TargetSeconds float64            `json:"vardiff_target_seconds"`
	FixedDiff     float64            `json:"fixed_diff"`
	Overrides     map[string]float64 `json:"worker_overrides"`
	// Start is the first difficulty of a new connection (vardiff starts
	// here); 0 in a saved file means "the configured initial difficulty".
	Start float64 `json:"start_diff"`
	// IgnorePasswordDiff and IgnoreSuggest make the pool's own settings
	// authoritative over a miner's "d=" password and its
	// mining.suggest_difficulty (some rental services send unsuitable ones).
	IgnorePasswordDiff bool `json:"ignore_password_diff"`
	IgnoreSuggest      bool `json:"ignore_suggest_difficulty"`
}

func validDiff(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= DiffFloor && v <= DiffCeiling
}

// Validate checks ranges and consistency.
func (d DiffSettings) Validate() error {
	var errs []error
	if !validDiff(d.Min) {
		errs = append(errs, fmt.Errorf("VARDIFF_MIN must be between %g and %g", DiffFloor, DiffCeiling))
	}
	if !validDiff(d.Max) {
		errs = append(errs, fmt.Errorf("VARDIFF_MAX must be between %g and %g", DiffFloor, DiffCeiling))
	}
	if validDiff(d.Min) && validDiff(d.Max) && d.Min > d.Max {
		errs = append(errs, errors.New("VARDIFF_MIN must be <= VARDIFF_MAX"))
	}
	if math.IsNaN(d.TargetSeconds) || d.TargetSeconds < MinTargetSeconds || d.TargetSeconds > MaxTargetSeconds {
		errs = append(errs, fmt.Errorf("VARDIFF_TARGET_SECONDS must be between %d and %d", MinTargetSeconds, MaxTargetSeconds))
	}
	if d.FixedDiff != 0 {
		if !validDiff(d.FixedDiff) {
			errs = append(errs, fmt.Errorf("FIXED_DIFF must be 0 (vardiff) or between %g and %g", DiffFloor, DiffCeiling))
		} else if d.FixedDiff < d.Min || d.FixedDiff > d.Max {
			errs = append(errs, errors.New("FIXED_DIFF must be within VARDIFF_MIN..VARDIFF_MAX"))
		}
	}
	if d.Start != 0 && !validDiff(d.Start) {
		errs = append(errs, fmt.Errorf("start difficulty must be 0 (configured default) or between %g and %g", DiffFloor, DiffCeiling))
	}
	if len(d.Overrides) > maxOverrides {
		errs = append(errs, fmt.Errorf("at most %d worker overrides", maxOverrides))
	}
	for w, v := range d.Overrides {
		if strings.TrimSpace(w) == "" || len(w) > maxWorkerNameBytes {
			errs = append(errs, errors.New("worker override names must be 1..256 bytes"))
		}
		if i := strings.IndexByte(w, '*'); i >= 0 && (i != len(w)-1 || i == 0) {
			errs = append(errs, fmt.Errorf("override %q: '*' may only end a name prefix, as in rental.*", w))
		}
		if !validDiff(v) {
			errs = append(errs, fmt.Errorf("override for %q must be between %g and %g", w, DiffFloor, DiffCeiling))
		}
	}
	return errors.Join(errs...)
}

// Clamp limits v to [Min, Max].
func (d DiffSettings) Clamp(v float64) float64 {
	v = math.Max(d.Min, math.Min(d.Max, v))
	if v >= 1 {
		v = math.Floor(v)
	}
	return v
}

// Clone returns a deep copy.
func (d DiffSettings) Clone() DiffSettings {
	c := d
	c.Overrides = make(map[string]float64, len(d.Overrides))
	for k, v := range d.Overrides {
		c.Overrides[k] = v
	}
	return c
}

// OverrideFor returns the override for the first worker that has one
// (workers in a stable order, primary first): an exact name first, else
// the longest prefix pattern ("rental.*") that matches.
func (d DiffSettings) OverrideFor(workers []string) (float64, bool) {
	for _, w := range workers {
		if v, ok := d.Overrides[w]; ok {
			return d.Clamp(v), true
		}
	}
	for _, w := range workers {
		best, found := "", false
		for k := range d.Overrides {
			if p, ok := strings.CutSuffix(k, "*"); ok && strings.HasPrefix(w, p) && len(p) > len(best) {
				best, found = p, true
			}
		}
		if found {
			return d.Clamp(d.Overrides[best+"*"]), true
		}
	}
	return 0, false
}

// StartDiff returns the first difficulty for a new connection.
func (d DiffSettings) StartDiff(configured float64) float64 {
	if d.Start > 0 {
		return d.Clamp(d.Start)
	}
	return d.Clamp(configured)
}

func sortedKeys(m map[string]bool, first string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k != first {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	if first != "" {
		out = append([]string{first}, out...)
	}
	return out
}
