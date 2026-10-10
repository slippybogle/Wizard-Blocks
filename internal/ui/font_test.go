package ui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every character the UI can render in the pixel font must have a glyph:
// all printable ASCII (lowercase maps to uppercase) and any non-ASCII
// character used in the scripts. A missing glyph would render as "?".
func TestPixelFontCoverage(t *testing.T) {
	src, err := os.ReadFile("static/js/font.js")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^\s+(?:'((?:\\\\)|[^'\\])'|"(.)"|([A-Z0-9])):`)
	have := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		k := m[1] + m[2] + m[3]
		if k == `\\` {
			k = `\`
		}
		have[k] = true
	}
	need := map[rune]bool{}
	for c := rune(32); c < 127; c++ {
		if c < 'a' || c > 'z' {
			need[c] = true
		}
	}
	for _, f := range []string{"app.js", "scene.js", "chart.js", "format.js", "settings.js"} {
		b, err := os.ReadFile("static/js/" + f)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range string(b) {
			if r > 127 {
				need[r] = true
			}
		}
	}
	for r := range need {
		if !have[string(r)] {
			t.Errorf("pixel font has no glyph for %q (U+%04X)", r, r)
		}
	}
}

// The JS colour palettes must have exactly ui.VariantCounts entries per tier.
func TestVariantPalettesMatch(t *testing.T) {
	b, err := staticFS.ReadFile("static/js/sprites.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "export const CREATURE_VARIANTS = [")
	j := strings.Index(src[i:], "\n];")
	if i < 0 || j < 0 {
		t.Fatal("CREATURE_VARIANTS not found")
	}
	tiers := strings.Split(src[i:i+j], "],")
	got := []int{}
	for _, tier := range tiers {
		if n := strings.Count(tier, "0x"); n > 0 {
			got = append(got, n)
		}
	}
	if len(got) != len(VariantCounts) {
		t.Fatalf("tiers: js %v, go %v", got, VariantCounts)
	}
	for k := range got {
		if got[k] != VariantCounts[k] {
			t.Fatalf("tier %d: js has %d colours, go expects %d", k, got[k], VariantCounts[k])
		}
	}
}
