package ui

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/node"
	"github.com/fladnagmai/wizard-blocks/internal/stats"
)

// The plain test page is served at /test/ (with /test redirecting there)
// for every coin and style, and its assets load from the same origin.
func TestTestPageServed(t *testing.T) {
	for _, style := range []string{"", "simple"} {
		s := New(Config{Coin: "ltc", Style: style}, stats.New("ltc", "t", ""), node.NewClient("http://127.0.0.1:1", "u", "p", "", time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err := s.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		go s.Serve()
		base := "http://" + s.Addr()
		for path, want := range map[string]string{"/test": "app.js", "/test/": "app.js", "/test/app.js": "api/state", "/test/style.css": "--bg"} {
			r, err := http.Get(base + path)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(r.Body)
			r.Body.Close()
			if r.StatusCode != 200 || !strings.Contains(string(b), want) {
				t.Fatalf("style %q %s: status %d, missing %q", style, path, r.StatusCode, want)
			}
		}
		s.Shutdown(context.Background())
	}
}
