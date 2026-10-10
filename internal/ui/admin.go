package ui

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/stratum"
)

// SettingsBackend applies and persists live difficulty settings (the engine).
type SettingsBackend interface {
	// DiffSettings returns the live settings, whether they come from a saved
	// file, and whether saving is possible at all (a data dir is set).
	DiffSettings() (s stratum.DiffSettings, saved bool, persistable bool)
	UpdateDiffSettings(stratum.DiffSettings) error
	ResetDiffSettings() error
	// Payout returns the fixed payout address ("" if not set yet) and
	// whether it can be set from the UI (fixed payout mode).
	Payout() (addr string, settable bool)
	// SetPayout verifies the address with the node, saves and applies it,
	// and returns its canonical form.
	SetPayout(ctx context.Context, addr string) (string, error)
}

// DogeBackend is implemented by an engine that can merge-mine Dogecoin.
type DogeBackend interface {
	// DogePayout returns the DOGE payout address ("" if none) and whether
	// merged mining is available (a Dogecoin node is configured).
	DogePayout() (addr string, available bool)
	// SetDogePayout verifies a new address with the Dogecoin node ("" turns
	// merged mining off), saves and applies it, and returns it.
	SetDogePayout(ctx context.Context, addr string) (string, error)
}

const (
	adminCookie   = "wb_admin"
	sessionTTL    = 12 * time.Hour
	loginWindow   = 5 * time.Minute
	loginMaxFails = 5
)

// admin implements password login and the settings endpoints. Settings live
// only on the UI listener; the Stratum and stats API ports never serve them.
//
// Settings are usable when the UI is behind an authenticating proxy (open),
// or when a password exists: one set from the Settings page (stored hashed
// in the data dir, it wins) or WB_UI_ADMIN_PASSWORD. Whenever a password
// exists, it is required for every settings request.
type admin struct {
	envPassword []byte
	open        bool
	pwPath      string // "" when there is no data dir
	backend     SettingsBackend

	mu     sync.Mutex
	secret []byte
	stored *pwHash
	fails  map[string][]time.Time // ip -> failed login times
}

func newAdmin(password string, open bool, pwPath string) *admin {
	a := &admin{envPassword: []byte(password), open: open, pwPath: pwPath, fails: map[string][]time.Time{}}
	a.rotate()
	return a
}

// rotate replaces the session key, ending every existing session.
func (a *admin) rotate() {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret) // sessions also end when the engine restarts
	a.mu.Lock()
	a.secret = secret
	a.mu.Unlock()
}

// SetSettingsBackend enables the settings API.
func (s *Server) SetSettingsBackend(b SettingsBackend) { s.admin.backend = b }

func (a *admin) storedHash() *pwHash {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stored
}

func (a *admin) required() bool { return a.storedHash() != nil || len(a.envPassword) > 0 }

func (a *admin) enabled() bool { return a.backend != nil && (a.open || a.required()) }

func (a *admin) checkPassword(pw string) bool {
	if h := a.storedHash(); h != nil {
		return h.verify(pw)
	}
	return len(a.envPassword) > 0 && subtle.ConstantTimeCompare([]byte(pw), a.envPassword) == 1
}

func (a *admin) sign(exp int64) string {
	a.mu.Lock()
	m := hmac.New(sha256.New, a.secret)
	a.mu.Unlock()
	m.Write([]byte(strconv.FormatInt(exp, 10)))
	return strconv.FormatInt(exp, 10) + "." + hex.EncodeToString(m.Sum(nil))
}

func (a *admin) setCookie(w http.ResponseWriter, r *http.Request) {
	exp := time.Now().Add(sessionTTL).Unix()
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Value: a.sign(exp), Path: "/api/admin", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: int(sessionTTL.Seconds()),
	})
}

func (a *admin) authed(r *http.Request) bool {
	if !a.enabled() {
		return false
	}
	if !a.required() {
		return true // open: the proxy in front already authenticated the user
	}
	c, err := r.Cookie(adminCookie)
	if err != nil {
		return false
	}
	expStr, _, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(a.sign(exp)), []byte(c.Value))
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// sameOrigin rejects cross-site requests: mutations must carry our custom
// header (which a cross-site form cannot set) and, if the browser sends an
// Origin, it must match the Host (or the X-Forwarded-Host set by a reverse
// proxy such as Umbrel's app_proxy).
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("X-WB-Admin") != "1" {
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
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (a *admin) rateLimited(ip string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	keep := a.fails[ip][:0]
	for _, t := range a.fails[ip] {
		if now.Sub(t) < loginWindow {
			keep = append(keep, t)
		}
	}
	a.fails[ip] = keep
	return len(keep) >= loginMaxFails
}

func (a *admin) recordFail(ip string, now time.Time) {
	a.mu.Lock()
	a.fails[ip] = append(a.fails[ip], now)
	if len(a.fails) > 10000 { // bound memory
		a.fails = map[string][]time.Time{ip: a.fails[ip]}
	}
	a.mu.Unlock()
}

func (s *Server) routesAdmin(mux *http.ServeMux) {
	a := s.admin
	mux.HandleFunc("/api/admin/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.session(r))
	})
	mux.HandleFunc("/api/admin/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !sameOrigin(r) {
			jsonErr(w, http.StatusForbidden, "forbidden")
			return
		}
		if !a.enabled() {
			jsonErr(w, http.StatusForbidden, "settings are disabled: set WB_UI_ADMIN_PASSWORD")
			return
		}
		if !a.required() {
			writeJSON(w, a.session(r))
			return
		}
		ip, now := clientIP(r), time.Now()
		if a.rateLimited(ip, now) {
			jsonErr(w, http.StatusTooManyRequests, "too many failed logins; try again in a few minutes")
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "bad request")
			return
		}
		if !a.checkPassword(body.Password) {
			a.recordFail(ip, now)
			time.Sleep(300 * time.Millisecond)
			jsonErr(w, http.StatusUnauthorized, "wrong password")
			return
		}
		a.setCookie(w, r)
		writeJSON(w, map[string]bool{"authed": true})
	})
	mux.HandleFunc("/api/admin/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !sameOrigin(r) {
			jsonErr(w, http.StatusForbidden, "forbidden")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: "", Path: "/api/admin", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		writeJSON(w, map[string]bool{"authed": false})
	})
	mux.HandleFunc("/api/admin/settings", func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(r) {
			jsonErr(w, http.StatusUnauthorized, "login required")
			return
		}
		switch r.Method {
		case http.MethodGet:
			a.writeSettings(w, s)
		case http.MethodPut:
			if !sameOrigin(r) {
				jsonErr(w, http.StatusForbidden, "forbidden")
				return
			}
			var d stratum.DiffSettings
			dec := json.NewDecoder(io.LimitReader(r.Body, 256<<10))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&d); err != nil {
				jsonErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
				return
			}
			if d.Overrides == nil {
				d.Overrides = map[string]float64{}
			}
			if err := d.Validate(); err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := a.backend.UpdateDiffSettings(d); err != nil {
				jsonErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			s.log.Info("difficulty settings changed from the UI", "ip", clientIP(r))
			a.writeSettings(w, s)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
	mux.HandleFunc("/api/admin/password", func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(r) {
			jsonErr(w, http.StatusUnauthorized, "login required")
			return
		}
		if !sameOrigin(r) {
			jsonErr(w, http.StatusForbidden, "forbidden")
			return
		}
		if a.pwPath == "" {
			jsonErr(w, http.StatusConflict, "no data directory: a settings password cannot be stored")
			return
		}
		switch r.Method {
		case http.MethodPost: // set or change
			var body struct {
				Password string `json:"password"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
				jsonErr(w, http.StatusBadRequest, "bad request")
				return
			}
			if n := len(body.Password); n < pwMinLen || n > pwMaxLen {
				jsonErr(w, http.StatusBadRequest, "password must be 8 to 256 characters")
				return
			}
			h, err := hashPassword(body.Password)
			if err == nil {
				err = savePwHash(a.pwPath, h)
			}
			if err != nil {
				jsonErr(w, http.StatusInternalServerError, "cannot save password: "+err.Error())
				return
			}
			a.mu.Lock()
			a.stored = h
			a.mu.Unlock()
			a.rotate()        // log out every other session
			a.setCookie(w, r) // keep this one logged in
			s.log.Info("settings password set from the UI", "ip", clientIP(r))
			writeJSON(w, map[string]any{"password_set": true, "password_required": true, "authed": true, "enabled": true, "can_set_password": true})
		case http.MethodDelete:
			if err := os.Remove(a.pwPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				jsonErr(w, http.StatusInternalServerError, "cannot remove password: "+err.Error())
				return
			}
			a.mu.Lock()
			a.stored = nil
			a.mu.Unlock()
			s.log.Info("settings password removed from the UI", "ip", clientIP(r))
			writeJSON(w, a.session(r))
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
	mux.HandleFunc("/api/admin/mount", func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(r) {
			jsonErr(w, http.StatusUnauthorized, "login required")
			return
		}
		if r.Method != http.MethodPost || !sameOrigin(r) {
			jsonErr(w, http.StatusForbidden, "forbidden")
			return
		}
		var body struct {
			BlockHash string `json:"block_hash"`
			Mount     bool   `json:"mount"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "bad request")
			return
		}
		o := s.BuildState(0, 0).MountOffer
		if o == nil || o.BlockHash != body.BlockHash {
			jsonErr(w, http.StatusConflict, "nothing to mount for this block")
			return
		}
		if err := s.mounts.decide(*o, body.Mount); err != nil {
			jsonErr(w, http.StatusConflict, err.Error())
			return
		}
		s.log.Info("trophy wall", "block", o.BlockHeight, "mounted", body.Mount, "tier", o.Tier, "pct", o.Pct)
		writeJSON(w, map[string]any{"mounts": s.mounts.mounts()})
	})
	mux.HandleFunc("/api/admin/payout", func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(r) {
			jsonErr(w, http.StatusUnauthorized, "login required")
			return
		}
		if r.Method != http.MethodPut || !sameOrigin(r) {
			jsonErr(w, http.StatusForbidden, "forbidden")
			return
		}
		var body struct {
			Address string `json:"address"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "bad request")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		addr, err := a.backend.SetPayout(ctx, body.Address)
		cancel()
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Info("payout address changed from the UI", "ip", clientIP(r), "address", addr)
		a.writeSettings(w, s)
	})
	mux.HandleFunc("/api/admin/doge-payout", func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(r) {
			jsonErr(w, http.StatusUnauthorized, "login required")
			return
		}
		if r.Method != http.MethodPut || !sameOrigin(r) {
			jsonErr(w, http.StatusForbidden, "forbidden")
			return
		}
		db, ok := a.backend.(DogeBackend)
		if !ok {
			jsonErr(w, http.StatusNotFound, "Dogecoin merged mining is not available")
			return
		}
		var body struct {
			Address string `json:"address"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "bad request")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		addr, err := db.SetDogePayout(ctx, body.Address)
		cancel()
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Info("DOGE payout address changed from the UI", "ip", clientIP(r), "address", addr)
		writeJSON(w, map[string]any{"doge_payout_address": addr})
	})
	mux.HandleFunc("/api/admin/settings/reset", func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(r) {
			jsonErr(w, http.StatusUnauthorized, "login required")
			return
		}
		if r.Method != http.MethodPost || !sameOrigin(r) {
			jsonErr(w, http.StatusForbidden, "forbidden")
			return
		}
		if err := a.backend.ResetDiffSettings(); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.log.Info("difficulty settings reset to config from the UI", "ip", clientIP(r))
		a.writeSettings(w, s)
	})
}

func (a *admin) session(r *http.Request) map[string]any {
	return map[string]any{
		"enabled": a.enabled(), "authed": a.authed(r),
		"password_set": a.storedHash() != nil, "password_required": a.required(),
		"env_password": len(a.envPassword) > 0, "can_set_password": a.pwPath != "",
	}
}

func (a *admin) writeSettings(w http.ResponseWriter, s *Server) {
	d, saved, persistable := a.backend.DiffSettings()
	workers := []string{}
	for _, wk := range s.st.Snapshot().Workers {
		workers = append(workers, wk.Name)
	}
	sort.Strings(workers)
	addr, settable := a.backend.Payout()
	writeJSON(w, map[string]any{
		"settings": d, "saved": saved, "persistable": persistable, "workers": workers,
		"payout": map[string]any{"address": addr, "settable": settable},
		"limits": map[string]float64{
			"diff_min": stratum.DiffFloor, "diff_max": stratum.DiffCeiling,
			"target_min": stratum.MinTargetSeconds, "target_max": stratum.MaxTargetSeconds,
		},
	})
}
