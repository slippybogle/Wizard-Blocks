package ui

import (
	"context"
	"encoding/json"
	"errors"
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
	payout  string
}

func (f *fakeBackend) Payout() (string, bool) { return f.payout, true }
func (f *fakeBackend) SetPayout(_ context.Context, addr string) (string, error) {
	if !strings.HasPrefix(addr, "bitcoincash:q") {
		return "", errors.New("invalid bch main address")
	}
	f.payout = addr
	return addr, nil
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

func TestPayoutFromSettings(t *testing.T) {
	s, fb, base := startUI(t, "pw")
	if st := s.BuildState(10, 10); st.Stratum.PayoutSet {
		t.Fatal("payout_set before an address was set")
	}
	c := newClient(t, base)
	if code, _ := c.do("PUT", "/api/admin/payout", `{"address":"bitcoincash:qtest"}`, true); code != 401 {
		t.Fatalf("unauthenticated payout change: %d", code)
	}
	if code, _ := c.do("POST", "/api/admin/login", `{"password":"pw"}`, true); code != 200 {
		t.Fatal("login")
	}
	if code, _ := c.do("PUT", "/api/admin/payout", `{"address":"bitcoincash:qtest"}`, false); code != 403 {
		t.Fatalf("payout change without the CSRF header: %d", code)
	}
	if code, m := c.do("PUT", "/api/admin/payout", `{"address":"1BadLegacy"}`, true); code != 400 || m["error"] == nil {
		t.Fatalf("invalid address accepted: %d %v", code, m)
	}
	code, m := c.do("PUT", "/api/admin/payout", `{"address":"bitcoincash:qtest"}`, true)
	if code != 200 || fb.payout != "bitcoincash:qtest" || m["payout"].(map[string]any)["address"] != "bitcoincash:qtest" {
		t.Fatalf("payout not set: %d %v", code, m)
	}
	if st := s.BuildState(10, 10); !st.Stratum.PayoutSet || st.Stratum.PayoutAddress != "bitcoincash:qtest" {
		t.Fatalf("state after set: %+v", st.Stratum)
	}
}

func TestOriginBehindProxy(t *testing.T) {
	r, _ := http.NewRequest("POST", "http://wb_web_1:8420/api/admin/login", nil)
	r.Header.Set("X-WB-Admin", "1")
	r.Header.Set("Origin", "http://umbrel.local:8420")
	if sameOrigin(r) {
		t.Fatal("mismatched origin accepted")
	}
	r.Header.Set("X-Forwarded-Host", "umbrel.local:8420")
	if !sameOrigin(r) {
		t.Fatal("proxied same-origin request refused")
	}
	r.Header.Set("Origin", "http://evil.example")
	if sameOrigin(r) {
		t.Fatal("cross-origin accepted behind proxy")
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
	cases := []struct {
		pct    float64
		tier   int
		rarity string
	}{
		{0, 0, "Common"}, {49.999, 0, "Common"},
		{50, 1, "Uncommon"}, {63.299, 1, "Uncommon"},
		{63.3, 2, "Rare"}, {76.699, 2, "Rare"},
		{76.7, 3, "Epic"}, {89.999, 3, "Epic"},
		{90, 4, "Legendary"}, {99.9999, 4, "Legendary"},
		{100, BlockTier, "Block"}, {4e8, BlockTier, "Block"},
	}
	for _, c := range cases {
		tier, rarity, name := creatureFor(c.pct)
		if tier != c.tier || rarity != c.rarity {
			t.Errorf("%g%%: tier %d %s (%s), want %d %s", c.pct, tier, rarity, name, c.tier, c.rarity)
		}
	}
	if _, _, name := creatureFor(95); name != "Legendary Dragon" {
		t.Errorf("Legendary creature is %q", name)
	}
}

func TestRoundViewsTierFromNetworkPct(t *testing.T) {
	s, _, _ := startUI(t, "")
	s.st.SetTemplate(func(ti *stats.TemplateInfo) { ti.Height, ti.PrevHash, ti.NetworkDiff = 10, "aa", 1000 })
	if r := s.BuildState(10, 10).Rounds[0]; !r.Current || r.Tier != 0 || r.Rarity != "Common" || r.PctOfNetwork != 0 {
		t.Fatalf("new job without shares: %+v", r)
	}
	s.st.ShareAccepted("w", 1, 700, "x") // 70% of network difficulty
	if r := s.BuildState(10, 10).Rounds[0]; r.Rarity != "Rare" || r.PctOfNetwork != 70 {
		t.Fatalf("70%% job: %+v", r)
	}
	s.st.ShareAccepted("w", 1, 950, "x") // 95%
	if r := s.BuildState(10, 10).Rounds[0]; r.Rarity != "Legendary" || r.Creature != "Legendary Dragon" {
		t.Fatalf("95%% job: %+v", r)
	}
	s.st.ShareAccepted("w", 1, 1000, "x") // 100%, but no block record
	if r := s.BuildState(10, 10).Rounds[0]; r.Tier != BlockTier || r.Creature != "Block (not accepted)" {
		t.Fatalf("100%% job without block: %+v", r)
	}
	s.st.BlockSubmitted(stats.BlockRecord{Height: 10, Hash: "bb", Status: "accepted"})
	if r := s.BuildState(10, 10).Rounds[0]; r.Creature != "Block found!" {
		t.Fatalf("100%% job with accepted block: %+v", r)
	}
	// A found block whose share computes to a hair under 100% is still a Block.
	s.st.SetTemplate(func(ti *stats.TemplateInfo) { ti.Height, ti.PrevHash, ti.NetworkDiff = 11, "cc", 1000 })
	s.st.ShareAccepted("w", 1, 999.9999999, "x")
	s.st.BlockSubmitted(stats.BlockRecord{Height: 11, Hash: "dd", Status: "accepted"})
	if r := s.BuildState(10, 10).Rounds[0]; r.Tier != BlockTier || r.Rarity != "Block" {
		t.Fatalf("found block at 99.99999%%: %+v", r)
	}
	s.st.SetTemplate(func(ti *stats.TemplateInfo) { ti.Height, ti.PrevHash, ti.NetworkDiff = 10, "aa2", 1000 })
	s.st.ShareAccepted("w", 1, 1000, "x")
	if d := s.BuildState(10, 10).Derived; d.BestThisJobPct != 100 {
		t.Fatalf("best_this_job_pct %v", d.BestThisJobPct)
	}
}

func TestLuckSinceLastBlock(t *testing.T) {
	s, _, _ := startUI(t, "")
	if s.BuildState(1, 1).Derived.LuckPct != nil {
		t.Fatal("luck before any share")
	}
	s.st.ShareAccepted("w", 10, 1000, "x") // exp(-10/1000) = 99.005%
	l := s.BuildState(1, 1).Derived.LuckPct
	if l == nil || *l < 99 || *l > 99.01 {
		t.Fatalf("luck %v", l)
	}
	s.st.SetTemplate(func(ti *stats.TemplateInfo) { ti.PrevHash = "next-job" })
	if l2 := s.BuildState(1, 1).Derived.LuckPct; l2 == nil || *l2 != *l {
		t.Fatal("luck changed on a new job")
	}
	s.st.BlockSubmitted(stats.BlockRecord{Hash: "x", Status: "accepted"})
	if s.BuildState(1, 1).Derived.LuckPct != nil {
		t.Fatal("luck not reset by a found block")
	}
}
