package ui

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/slippybogle/wizard-blocks/internal/stratum"
)

// SettingsBackend applies and persists live difficulty settings (the engine).
type SettingsBackend interface {
	// DiffSettings returns the live settings, whether they come from a saved
	// file, and whether saving is possible at all (a data dir is set).
	DiffSettings() (s stratum.DiffSettings, saved bool, persistable bool)
	UpdateDiffSettings(stratum.DiffSettings) error
	ResetDiffSettings() error
}

const (
	adminCookie   = "wb_admin"
	sessionTTL    = 12 * time.Hour
	loginWindow   = 5 * time.Minute
	loginMaxFails = 5
)

// admin implements password login and the settings endpoints. Settings live
// only on the UI listener; the Stratum and stats API ports never serve them.
type admin struct {
	password []byte
	secret   []byte
	backend  SettingsBackend

	mu    sync.Mutex
	fails map[string][]time.Time // ip -> failed login times
}

func newAdmin(password string) *admin {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret) // sessions end when the engine restarts
	return &admin{password: []byte(password), secret: secret, fails: map[string][]time.Time{}}
}

// SetSettingsBackend enables the settings API.
func (s *Server) SetSettingsBackend(b SettingsBackend) { s.admin.backend = b }

func (a *admin) enabled() bool { return len(a.password) > 0 && a.backend != nil }

func (a *admin) sign(exp int64) string {
	m := hmac.New(sha256.New, a.secret)
	m.Write([]byte(strconv.FormatInt(exp, 10)))
	return strconv.FormatInt(exp, 10) + "." + hex.EncodeToString(m.Sum(nil))
}

func (a *admin) authed(r *http.Request) bool {
	if !a.enabled() {
		return false
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
// Origin, it must match the Host.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("X-WB-Admin") != "1" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
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
		writeJSON(w, map[string]bool{"enabled": a.enabled(), "authed": a.authed(r)})
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
		if subtle.ConstantTimeCompare([]byte(body.Password), a.password) != 1 {
			a.recordFail(ip, now)
			time.Sleep(300 * time.Millisecond)
			jsonErr(w, http.StatusUnauthorized, "wrong password")
			return
		}
		exp := now.Add(sessionTTL).Unix()
		http.SetCookie(w, &http.Cookie{
			Name: adminCookie, Value: a.sign(exp), Path: "/api/admin", HttpOnly: true,
			SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: int(sessionTTL.Seconds()),
		})
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

func (a *admin) writeSettings(w http.ResponseWriter, s *Server) {
	d, saved, persistable := a.backend.DiffSettings()
	workers := []string{}
	for _, wk := range s.st.Snapshot().Workers {
		workers = append(workers, wk.Name)
	}
	sort.Strings(workers)
	writeJSON(w, map[string]any{
		"settings": d, "saved": saved, "persistable": persistable, "workers": workers,
		"limits": map[string]float64{
			"diff_min": stratum.DiffFloor, "diff_max": stratum.DiffCeiling,
			"target_min": stratum.MinTargetSeconds, "target_max": stratum.MaxTargetSeconds,
		},
	})
}
