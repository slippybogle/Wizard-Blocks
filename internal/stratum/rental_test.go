package stratum

import (
	"encoding/json"
	"testing"
)

func suggest(s *Session, d float64) {
	b, _ := json.Marshal(map[string]any{"id": 9, "method": "mining.suggest_difficulty", "params": []float64{d}})
	s.handleLine(b)
}

// The precedence of docs/DESIGN-PHASE4.md section 3, case by case.
func TestRentalDifficultyPrecedence(t *testing.T) {
	cfg := testConfig() // vardiff min 1, max 1e12, initial 1024
	s := testServer(t, cfg)
	set := func(f func(*DiffSettings)) {
		d := s.DiffSettings()
		f(&d)
		if err := s.SetDiffSettings(d); err != nil {
			t.Fatal(err)
		}
	}

	// Start difficulty: the first job of every new connection.
	set(func(d *DiffSettings) { d.Start = 262144 })
	if d := diffOf(authorizeSession(t, s, "dghome", "x")); d != 262144 {
		t.Fatalf("start difficulty: %v", d)
	}
	// Miner d= wins over start and vardiff.
	if d := diffOf(authorizeSession(t, s, "dghome2", "d=200000")); d != 200000 {
		t.Fatalf("d=200000: %v", d)
	}
	// suggest_difficulty moves the starting point (vardiff continues).
	sg := authorizeSession(t, s, "sg", "x")
	suggest(sg, 500000)
	if d := diffOf(sg); d != 500000 {
		t.Fatalf("suggest_difficulty: %v", d)
	}

	// Prefix override: rental.* covers changing rig names, not "rentalx".
	set(func(d *DiffSettings) { d.Overrides = map[string]float64{"rental.*": 2e6, "rental.rig9": 3e6} })
	if d := diffOf(authorizeSession(t, s, "rental.rig1", "d=1000")); d != 2e6 {
		t.Fatalf("rental.* override (over d=): %v", d)
	}
	if d := diffOf(authorizeSession(t, s, "rental.rig2", "x")); d != 2e6 {
		t.Fatalf("rental.* override for a second rig: %v", d)
	}
	if d := diffOf(authorizeSession(t, s, "rental.rig9", "x")); d != 3e6 {
		t.Fatalf("exact name beats the prefix: %v", d)
	}
	if d := diffOf(authorizeSession(t, s, "rentalx", "x")); d != 262144 {
		t.Fatalf("rentalx must not match rental.*: %v", d)
	}
	// Longest prefix wins.
	set(func(d *DiffSettings) { d.Overrides = map[string]float64{"rental.*": 2e6, "rental.big.*": 8e6} })
	if d := diffOf(authorizeSession(t, s, "rental.big.1", "x")); d != 8e6 {
		t.Fatalf("longest prefix: %v", d)
	}

	// Switches off: the pool's settings are authoritative.
	set(func(d *DiffSettings) { d.Overrides = nil; d.IgnorePasswordDiff = true; d.IgnoreSuggest = true })
	if d := diffOf(authorizeSession(t, s, "nh1", "d=1000")); d != 262144 {
		t.Fatalf("d= honoured with the switch off: %v", d)
	}
	ig := authorizeSession(t, s, "nh2", "x")
	suggest(ig, 77)
	if d := diffOf(ig); d != 262144 {
		t.Fatalf("suggest_difficulty honoured with the switch off: %v", d)
	}
	// Turning the d= switch back on applies to connected miners at once.
	pw := authorizeSession(t, s, "nh3", "d=4096")
	set(func(d *DiffSettings) { d.IgnorePasswordDiff = false })
	if d := diffOf(pw); d != 4096 {
		t.Fatalf("live switch: %v", d)
	}

	// Fixed mode below overrides and d=, above vardiff.
	set(func(d *DiffSettings) { d.FixedDiff = 65536 })
	if d := diffOf(authorizeSession(t, s, "f1", "x")); d != 65536 {
		t.Fatalf("fixed: %v", d)
	}
	// Clamping: start above max is clamped.
	set(func(d *DiffSettings) { d.FixedDiff = 0; d.Max = 100000; d.Start = 262144 })
	if d := diffOf(authorizeSession(t, s, "c1", "x")); d != 100000 {
		t.Fatalf("start clamped to max: %v", d)
	}
}

func TestRentalDiffSettingsValidate(t *testing.T) {
	base := testDS(testConfig().Vardiff)
	bad := []func(*DiffSettings){
		func(d *DiffSettings) { d.Overrides = map[string]float64{"*": 1000} },
		func(d *DiffSettings) { d.Overrides = map[string]float64{"ren*tal": 1000} },
		func(d *DiffSettings) { d.Overrides = map[string]float64{"*rental": 1000} },
		func(d *DiffSettings) { d.Start = -1 },
		func(d *DiffSettings) { d.Start = 1e30 },
	}
	for i, f := range bad {
		d := base.Clone()
		f(&d)
		if d.Validate() == nil {
			t.Errorf("case %d accepted: %+v", i, d)
		}
	}
	ok := base.Clone()
	ok.Overrides = map[string]float64{"rental.*": 1e6, "rig1": 1e3}
	ok.Start = 262144
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	// Settings saved before Phase 4 (no start_diff) fall back to the
	// configured initial difficulty.
	var old DiffSettings
	if err := json.Unmarshal([]byte(`{"vardiff_min":1,"vardiff_max":1e12,"vardiff_target_seconds":10,"fixed_diff":0,"worker_overrides":{}}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Validate() != nil || old.StartDiff(1024) != 1024 || old.IgnorePasswordDiff || old.IgnoreSuggest {
		t.Fatalf("old settings: %+v", old)
	}
}
