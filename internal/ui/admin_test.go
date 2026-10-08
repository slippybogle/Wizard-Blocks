package ui

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/slippybogle/wizard-blocks/internal/node"
	"github.com/slippybogle/wizard-blocks/internal/stats"
	"github.com/slippybogle/wizard-blocks/internal/stratum"
)

// fakeBackend stands in for the engine in these HTTP-level tests.
type fakeBackend struct {
	d       stratum.DiffSettings
	updates int
	resets  int
}

func (f *fakeBackend) DiffSettings() (stratum.DiffSettings, bool, bool) {
	return f.d.Clone(), f.updates > 0, true
}
func (f *fakeBackend) UpdateDiffSettings(d stratum.DiffSettings) error {
	f.d = d
	f.updates++
	return nil
}
func (f *fakeBackend) ResetDiffSettings() error { f.resets++; return nil }

func startUI(t *testing.T, password string) (*Server, *fakeBackend, string) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(Config{Coin: "bch", AdminPassword: password}, stats.New("bch", "t", ""), node.NewClient("http://127.0.0.1:1", "u", "p", "", time.Second), log)
	fb := &fakeBackend{d: stratum.DiffSettings{Min: 1, Max: 1e12, TargetSeconds: 10, Overrides: map[string]float64{}}}
	s.SetSettingsBackend(fb)
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	return s, fb, "http://" + s.Addr()
}

type client struct {
	t    *testing.T
	base string
	hc   *http.Client
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, hc: &http.Client{Jar: jar}}
}

func (c *client) do(method, path, body string, adminHeader bool) (int, map[string]any) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if adminHeader {
		req.Header.Set("X-WB-Admin", "1")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

func TestSettingsDisabledWithoutPassword(t *testing.T) {
	_, _, base := startUI(t, "")
	c := newClient(t, base)
	if _, m := c.do("GET", "/api/admin/session", "", false); m["enabled"] != false {
		t.Fatalf("session %v", m)
	}
	if code, _ := c.do("POST", "/api/admin/login", `{"password":""}`, true); code != http.StatusForbidden {
		t.Fatalf("login with settings disabled: %d", code)
	}
	if code, _ := c.do("GET", "/api/admin/settings", "", false); code != http.StatusUnauthorized {
		t.Fatalf("settings readable without auth: %d", code)
	}
}

func TestSettingsAuthFlow(t *testing.T) {
	_, fb, base := startUI(t, "s3cret-pass")
	c := newClient(t, base)
	if code, _ := c.do("GET", "/api/admin/settings", "", false); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET: %d", code)
	}
	if code, _ := c.do("PUT", "/api/admin/settings", `{"vardiff_min":1,"vardiff_max":2,"vardiff_target_seconds":10}`, true); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated PUT: %d", code)
	}
	if code, _ := c.do("POST", "/api/admin/login", `{"password":"s3cret-pass"}`, false); code != http.StatusForbidden {
		t.Fatalf("login without CSRF header: %d", code)
	}
	if code, _ := c.do("POST", "/api/admin/login", `{"password":"wrong"}`, true); code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", code)
	}
	if code, _ := c.do("POST", "/api/admin/login", `{"password":"s3cret-pass"}`, true); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	code, m := c.do("GET", "/api/admin/settings", "", false)
	if code != http.StatusOK || m["persistable"] != true {
		t.Fatalf("GET settings: %d %v", code, m)
	}
	// Mutations need the custom header even with a valid session.
	good := `{"vardiff_min":16,"vardiff_max":65536,"vardiff_target_seconds":15,"fixed_diff":0,"worker_overrides":{"rig.1":512}}`
	if code, _ := c.do("PUT", "/api/admin/settings", good, false); code != http.StatusForbidden {
		t.Fatalf("PUT without CSRF header: %d", code)
	}
	for _, bad := range []string{
		`{"vardiff_min":100,"vardiff_max":10,"vardiff_target_seconds":15}`,
		`{"vardiff_min":1,"vardiff_max":10,"vardiff_target_seconds":0}`,
		`{"vardiff_min":1,"vardiff_max":10,"vardiff_target_seconds":10,"fixed_diff":50}`,
		`{"vardiff_min":1,"vardiff_max":10,"vardiff_target_seconds":10,"worker_overrides":{"x":-1}}`,
		`{"vardiff_min":1,"vardiff_max":10,"vardiff_target_seconds":10,"unknown":1}`,
		`not json`,
	} {
		if code, _ := c.do("PUT", "/api/admin/settings", bad, true); code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, code)
		}
	}
	if fb.updates != 0 {
		t.Fatal("invalid settings reached the backend")
	}
	if code, m := c.do("PUT", "/api/admin/settings", good, true); code != http.StatusOK {
		t.Fatalf("valid PUT: %d %v", code, m)
	}
	if fb.updates != 1 || fb.d.Min != 16 || fb.d.Overrides["rig.1"] != 512 {
		t.Fatalf("backend got %+v", fb.d)
	}
	if code, _ := c.do("POST", "/api/admin/settings/reset", "", true); code != http.StatusOK || fb.resets != 1 {
		t.Fatalf("reset: %d", code)
	}
	if code, _ := c.do("POST", "/api/admin/logout", "", true); code != http.StatusOK {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := c.do("GET", "/api/admin/settings", "", false); code != http.StatusUnauthorized {
		t.Fatalf("GET after logout: %d", code)
	}
}

func TestSettingsCookieTampering(t *testing.T) {
	s, _, base := startUI(t, "pw")
	try := func(v string) int {
		req, _ := http.NewRequest("GET", base+"/api/admin/settings", nil)
		req.AddCookie(&http.Cookie{Name: adminCookie, Value: v})
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	valid := s.admin.sign(time.Now().Add(time.Hour).Unix())
	if try(valid) != http.StatusOK {
		t.Fatal("valid token refused")
	}
	expired := s.admin.sign(time.Now().Add(-time.Minute).Unix())
	forged := strings.Replace(valid, valid[:5], "99999", 1)
	for _, v := range []string{expired, forged, "garbage", valid[:len(valid)-2] + "00"} {
		if code := try(v); code != http.StatusUnauthorized {
			t.Errorf("token %q accepted (%d)", v, code)
		}
	}
}

func TestLoginRateLimit(t *testing.T) {
	_, _, base := startUI(t, "pw")
	c := newClient(t, base)
	for i := 0; i < loginMaxFails; i++ {
		c.do("POST", "/api/admin/login", `{"password":"nope"}`, true)
	}
	if code, _ := c.do("POST", "/api/admin/login", `{"password":"pw"}`, true); code != http.StatusTooManyRequests {
		t.Fatalf("not rate limited: %d", code)
	}
}

func TestCrossOriginRejected(t *testing.T) {
	_, _, base := startUI(t, "pw")
	req, _ := http.NewRequest("POST", base+"/api/admin/login", strings.NewReader(`{"password":"pw"}`))
	req.Header.Set("X-WB-Admin", "1")
	req.Header.Set("Origin", "http://evil.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin login: %d", res.StatusCode)
	}
}

func TestStaticAndState(t *testing.T) {
	_, _, base := startUI(t, "")
	for _, p := range []string{"/", "/js/app.js", "/css/app.css", "/api/state"} {
		res, err := http.Get(base + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 || res.Header.Get("Content-Security-Policy") == "" {
			t.Errorf("%s: %d csp=%q", p, res.StatusCode, res.Header.Get("Content-Security-Policy"))
		}
	}
}

func TestCreatureTiers(t *testing.T) {
	cases := map[float64]string{1e-7: "Cave Mite", 5e-4: "Rock Bat", 0.5: "Mine Troll", 99: "Ancient Wyrm", 100: "Block Dragon", 4e8: "Block Dragon"}
	for pct, want := range cases {
		if _, got := creatureFor(pct); got != want {
			t.Errorf("%g%%: %s want %s", pct, got, want)
		}
	}
}
