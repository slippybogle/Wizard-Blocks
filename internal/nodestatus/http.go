package nodestatus

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

// Handler serves the page (web/), GET /api/status (the latest statuses) and
// POST /api/nodes/{key}/sync {"paused": bool} (pause or resume a pausable
// node's sync). It never exposes RPC credentials (statuses carry none).
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
	mux.HandleFunc("POST /api/nodes/{key}/sync", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			jsonErr(w, http.StatusForbidden, "forbidden")
			return
		}
		var body struct {
			Paused *bool `json:"paused"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body); err != nil || body.Paused == nil {
			jsonErr(w, http.StatusBadRequest, `body must be {"paused": true|false}`)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		err := p.SetPaused(ctx, r.PathValue("key"), *body.Paused)
		switch {
		case errors.Is(err, ErrUnknownNode):
			jsonErr(w, http.StatusNotFound, err.Error())
			return
		case errors.Is(err, ErrNotPausable):
			jsonErr(w, http.StatusForbidden, err.Error())
			return
		case err != nil && strings.HasPrefix(err.Error(), "cannot save"):
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{"paused": *body.Paused}
		if err != nil {
			resp["warning"] = err.Error()
		}
		json.NewEncoder(w).Encode(resp)
	})
	// no-store: a rebuilt image (same version) always serves its own page.
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	}))
	return secure(mux)
}

// sameOrigin rejects cross-site requests: the page sends a custom header
// (which a cross-site form cannot set) and, when the browser sends an
// Origin, it must match the Host or the X-Forwarded-Host set by Umbrel's
// app_proxy.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("X-NS-Action") != "1" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil {
			return false
		}
		fwd, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Host"), ",")
		if u.Host != r.Host && (fwd == "" || u.Host != strings.TrimSpace(fwd)) {
			return false
		}
	}
	return true
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
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
