package nodestatus

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"time"
)

//go:embed web
var webFS embed.FS

// Handler serves the page (web/) and GET /api/status: the latest statuses.
// It never exposes RPC credentials (statuses carry none).
func Handler(p *Poller, version string) http.Handler {
	sub, _ := fs.Sub(webFS, "web")
	files := http.FileServer(http.FS(sub))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(struct {
			Version string   `json:"version"`
			Now     int64    `json:"now"`
			Nodes   []Status `json:"nodes"`
		}{version, time.Now().Unix(), p.Snapshot()})
	})
	// no-store: a rebuilt image (same version) always serves its own page.
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	}))
	return secure(mux)
}

// secure keeps the page self-contained: no third-party content, no inline
// code, no framing.
func secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
}
