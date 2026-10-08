package ui

import (
	"os"
	"regexp"
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
