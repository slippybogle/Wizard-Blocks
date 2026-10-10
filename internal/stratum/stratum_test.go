package stratum

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/address"
	"github.com/fladnagmai/wizard-blocks/internal/node"
	"github.com/fladnagmai/wizard-blocks/internal/stats"
	"github.com/fladnagmai/wizard-blocks/internal/work"
)

func testConfig() Config {
	return Config{
		Listen: "127.0.0.1:0", Extranonce2Size: 8, VersionRollingMask: 0x1fffe000,
		MaxConnections: 100, MaxConnsPerIP: 2, AuthTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second,
		MaxLineBytes: 4096, MsgRate: 1000, MsgBurst: 1000,
		Vardiff: VardiffConfig{Initial: 1024, Min: 1, Max: 1e12, TargetShare: 10 * time.Second, Retarget: 60 * time.Second, VariancePct: 30},
	}
}

func testServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := stats.New("btc", "test", "")
	p, _ := work.ParamsFor(address.BTC)
	// The manager is never Run: no RPC traffic happens in these tests.
	mgr := work.NewManager(work.Config{Params: p, Chain: "regtest", Extranonce2Size: 8,
		PollInterval: time.Second, RefreshInterval: time.Second}, node.NewClient("http://127.0.0.1:1", "u", "p", "", time.Second), st, log)
	resolve := func(ctx context.Context, user string) (*Payout, error) {
		if strings.HasPrefix(user, "bad") {
			return nil, io.ErrUnexpectedEOF
		}
		return &Payout{Script: []byte{0x51}, Address: "test"}, nil
	}
	return NewServer(cfg, mgr, resolve, st, log)
}

// pipeSession returns a session on one end of a pipe and a reader for the other.
func pipeSession(t *testing.T, s *Server) (*Session, *bufio.Reader, net.Conn) {
	a, b := net.Pipe()
	sess := newSession(s, a, "127.0.0.1", 0x01020304)
	s.mu.Lock()
	s.sessions[sess] = struct{}{} // as admit() would
	s.mu.Unlock()
	go sess.writer()
	t.Cleanup(func() { sess.close("test"); b.Close() })
	return sess, bufio.NewReader(b), b
}

func readMsg(t *testing.T, r *bufio.Reader) map[string]any {
	t.Helper()
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("bad json %q", line)
	}
	return m
}

func TestSubscribeConfigureAuthorize(t *testing.T) {
	s := testServer(t, testConfig())
	sess, r, _ := pipeSession(t, s)

	go sess.handleLine([]byte(`{"id":1,"method":"mining.configure","params":[["version-rolling","minimum-difficulty"],{"version-rolling.mask":"ffffffff","version-rolling.min-bit-count":2}]}`))
	m := readMsg(t, r)
	res := m["result"].(map[string]any)
	if res["version-rolling"] != true || res["version-rolling.mask"] != "1fffe000" || res["minimum-difficulty"] != false {
		t.Fatalf("configure result %v", res)
	}
	go sess.handleLine([]byte(`{"id":2,"method":"mining.configure","params":[["version-rolling"],{"version-rolling.mask":"00ffe000"}]}`))
	if res := readMsg(t, r)["result"].(map[string]any); res["version-rolling.mask"] != "00ffe000" {
		t.Fatalf("mask intersection %v", res)
	}

	go sess.handleLine([]byte(`{"id":"abc","method":"mining.subscribe","params":["cgminer/4.10"]}`))
	m = readMsg(t, r)
	if m["id"] != "abc" {
		t.Fatalf("id not echoed: %v", m["id"])
	}
	sub := m["result"].([]any)
	if sub[1] != "01020304" || sub[2].(float64) != 8 {
		t.Fatalf("subscribe result %v", sub)
	}

	go sess.handleLine([]byte(`{"id":3,"method":"mining.authorize","params":["bad.worker","x"]}`))
	m = readMsg(t, r)
	if m["result"] != false || m["error"].([]any)[0].(float64) != ErrUnauthorized {
		t.Fatalf("bad authorize %v", m)
	}
	go sess.handleLine([]byte(`{"id":4,"method":"mining.authorize","params":["rig.1","d=4096"]}`))
	if m = readMsg(t, r); m["result"] != true {
		t.Fatalf("authorize %v", m)
	}
	if sess.diff != 4096 {
		t.Fatalf("password difficulty not applied: %v", sess.diff)
	}
	// No work exists (manager not running): a submit finds no job.
	go sess.handleLine([]byte(`{"id":5,"method":"mining.submit","params":["rig.1","1","0000000000000000","00000000","00000000"]}`))
	if m = readMsg(t, r); m["error"].([]any)[0].(float64) != ErrJobNotFound {
		t.Fatalf("submit %v", m)
	}
	go sess.handleLine([]byte(`{"id":6,"method":"mining.submit","params":["other","1","0000000000000000","00000000","00000000"]}`))
	if m = readMsg(t, r); m["error"].([]any)[0].(float64) != ErrUnauthorized {
		t.Fatalf("submit other worker %v", m)
	}
}

func TestMalformedInput(t *testing.T) {
	s := testServer(t, testConfig())
	sess, r, _ := pipeSession(t, s)
	go func() {
		for i := 0; i < 5; i++ {
			sess.handleLine([]byte(`{not json`))
		}
	}()
	for i := 0; i < 5; i++ {
		if m := readMsg(t, r); m["error"] == nil {
			t.Fatal("expected error reply")
		}
	}
	// Responses (no method) are ignored, unknown methods get an error.
	go func() {
		sess.handleLine([]byte(`{"id":9,"result":true,"error":null}`))
		sess.handleLine([]byte(`{"id":10,"method":"mining.bogus","params":[]}`))
	}()
	if m := readMsg(t, r); m["id"].(float64) != 10 {
		t.Fatalf("unexpected %v", m)
	}
	// More than 10 malformed messages disconnects (handleLine runs on one
	// goroutine, as in the real reader loop; replies are drained concurrently).
	go io.Copy(io.Discard, r)
	res := make(chan bool)
	go func() {
		ok := true
		for i := 0; i < 20 && ok; i++ {
			ok = sess.handleLine([]byte(`[`))
		}
		res <- ok
	}()
	if <-res {
		t.Fatal("not disconnected after repeated garbage")
	}
}

func FuzzHandleLine(f *testing.F) {
	seeds := []string{
		`{"id":1,"method":"mining.subscribe","params":[]}`,
		`{"id":1,"method":"mining.authorize","params":["a","b"]}`,
		`{"id":1,"method":"mining.submit","params":["a","1","00","00000000","00000000","1fffe000"]}`,
		`{"id":1,"method":"mining.configure","params":[["version-rolling"],{"version-rolling.mask":"zz"}]}`,
		`{"id":1,"method":"mining.suggest_difficulty","params":[-1]}`,
		`{"id":null,"method":"mining.submit","params":null}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	srv := testServer(&testing.T{}, testConfig())
	f.Fuzz(func(t *testing.T, line string) {
		a, b := net.Pipe()
		go io.Copy(io.Discard, b)
		sess := newSession(srv, a, "127.0.0.1", 1)
		go sess.writer()
		sess.handleLine([]byte(`{"id":0,"method":"mining.subscribe","params":[]}`))
		sess.handleLine([]byte(line))
		sess.close("done")
		b.Close()
	})
}

func TestSlowClientDisconnected(t *testing.T) {
	s := testServer(t, testConfig())
	a, b := net.Pipe() // nobody reads b: writes block forever
	defer b.Close()
	sess := newSession(s, a, "127.0.0.1", 7)
	go sess.writer()
	for i := 0; i < 200; i++ {
		sess.notify("mining.set_difficulty", []any{1})
	}
	select {
	case <-sess.done:
	case <-time.After(5 * time.Second):
		t.Fatal("slow client was not disconnected")
	}
}

func TestRateLimit(t *testing.T) {
	cfg := testConfig()
	cfg.MsgRate, cfg.MsgBurst = 1, 5
	s := testServer(t, cfg)
	a, b := net.Pipe()
	defer b.Close()
	sess := newSession(s, a, "127.0.0.1", 8)
	n := 0
	for sess.allow() {
		n++
		if n > 100 {
			break
		}
	}
	if n != 5 {
		t.Fatalf("burst allowed %d messages, want 5", n)
	}
}

// Accepted shares give their token back: a miner sending only valid shares
// is never cut off by the limiter, however fast; anything else still counts.
func TestRateLimitSparesAcceptedShares(t *testing.T) {
	cfg := testConfig()
	cfg.MsgRate, cfg.MsgBurst = 1, 5
	s := testServer(t, cfg)
	a, b := net.Pipe()
	defer b.Close()
	sess := newSession(s, a, "127.0.0.1", 8)
	for i := 0; i < 1000; i++ {
		if !sess.allow() {
			t.Fatalf("valid share %d refused by the limiter", i)
		}
		sess.refundToken() // accepted
	}
	n := 0
	for sess.allow() && n <= 100 {
		n++ // e.g. rejected shares or junk
	}
	if n != 5 {
		t.Fatalf("after valid shares, %d other messages allowed, want the burst of 5", n)
	}
}

func TestConnectionLimits(t *testing.T) {
	s := testServer(t, testConfig()) // 2 per IP
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx)
	defer s.Close()
	var conns []net.Conn
	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp", s.Addr())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conns = append(conns, c)
	}
	// The third connection is closed by the server immediately.
	_ = conns[2].SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conns[2].Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("third connection not refused: %v", err)
	}
	// The first two are served normally.
	_, _ = conns[0].Write([]byte(`{"id":1,"method":"mining.subscribe","params":[]}` + "\n"))
	_ = conns[0].SetReadDeadline(time.Now().Add(3 * time.Second))
	if line, err := bufio.NewReader(conns[0]).ReadString('\n'); err != nil || !strings.Contains(line, "mining.notify") {
		t.Fatalf("subscribe on allowed connection: %q %v", line, err)
	}
	// Oversized lines disconnect.
	_, _ = conns[1].Write([]byte(strings.Repeat("x", 5000) + "\n"))
	_ = conns[1].SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conns[1].Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Fatalf("oversized line not disconnected: %v", err)
	}
}

func TestAuthTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.AuthTimeout = 300 * time.Millisecond
	s := testServer(t, cfg)
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx)
	defer s.Close()
	c, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("idle unauthenticated connection not closed: %v", err)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("closed after %v", el)
	}
}

func TestVardiffConverges(t *testing.T) {
	cfg := testConfig().Vardiff
	for _, hashrate := range []float64{5e11, 1.2e12, 1e14, 2e15} {
		now := time.Unix(1_700_000_000, 0)
		v := newVardiff(cfg, now)
		cur := cfg.Initial
		for i := 0; i < 2000; i++ {
			// Expected time to the next share at difficulty cur.
			now = now.Add(time.Duration(cur * 4294967296 / hashrate * float64(time.Second)))
			if nd, ok := v.onShare(now, cur, cur, testDS(cfg)); ok {
				cur = nd
			}
		}
		ideal := hashrate * cfg.TargetShare.Seconds() / 4294967296
		if math.Abs(cur/ideal-1) > 0.35 {
			t.Errorf("hashrate %g: diff %g, ideal %g", hashrate, cur, ideal)
		}
	}
}

func TestVardiffQuietMinerDecreases(t *testing.T) {
	cfg := testConfig().Vardiff
	now := time.Unix(1_700_000_000, 0)
	v := newVardiff(cfg, now)
	cur := 1e6
	for i := 0; i < 10; i++ {
		now = now.Add(2*cfg.Retarget + time.Second)
		if nd, ok := v.onTick(now, cur, testDS(cfg)); ok {
			if nd >= cur {
				t.Fatal("tick did not lower difficulty")
			}
			cur = nd
		}
	}
	if cur > 1e6/1000 {
		t.Fatalf("difficulty still %g after long silence", cur)
	}
	// Never below min.
	for i := 0; i < 100; i++ {
		now = now.Add(2*cfg.Retarget + time.Second)
		if nd, ok := v.onTick(now, cur, testDS(cfg)); ok {
			cur = nd
		}
	}
	if cur != cfg.Min {
		t.Fatalf("difficulty %g, want clamp at min %g", cur, cfg.Min)
	}
}

func TestParsePasswordDiff(t *testing.T) {
	cases := map[string]float64{"d=1024": 1024, "x,d=0.5": 0.5, "x d=65536": 65536}
	for in, want := range cases {
		if d, ok := parsePasswordDiff(in); !ok || d != want {
			t.Errorf("%q: %v %v", in, d, ok)
		}
	}
	for _, in := range []string{"x", "d=", "d=-5", "d=abc", "d=Inf"} {
		if _, ok := parsePasswordDiff(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

func TestVersionCandidates(t *testing.T) {
	const mask = 0x1fffe000
	// Normal mainnet case: all interpretations coincide.
	c := versionCandidates(0x20000000, mask, 0x0aa00000)
	if len(c) != 1 || c[0].version != 0x2aa00000 || c[0].interp != "bip310+xor+or" {
		t.Fatalf("%+v", c)
	}
	// Template signalling bit 28 (inside the mask).
	c = versionCandidates(0x30000000, mask, 0x0aa00000)
	want := map[string]uint32{"bip310": 0x2aa00000, "xor": 0x3aa00000, "or": 0x3aa00000}
	for _, vc := range c {
		for _, n := range strings.Split(vc.interp, "+") {
			if want[n] != vc.version {
				t.Fatalf("%s -> %08x, want %08x", n, vc.version, want[n])
			}
			delete(want, n)
		}
	}
	if len(want) != 0 || len(c) != 2 {
		t.Fatalf("missing interpretations %v in %+v", want, c)
	}
	// Bits overlapping the signalled bit: xor clears it, or keeps it.
	c = versionCandidates(0x30000000, mask, 0x10002000)
	got := map[string]uint32{}
	for _, vc := range c {
		for _, n := range strings.Split(vc.interp, "+") {
			got[n] = vc.version
		}
	}
	if got["bip310"] != 0x30002000 || got["xor"] != 0x20002000 || got["or"] != 0x30002000 {
		t.Fatalf("%+v", got)
	}
	if !HasInterp("bip310+or", "or") || HasInterp("bip310+or", "xor") {
		t.Fatal("HasInterp")
	}
}

func testDS(cfg VardiffConfig) DiffSettings {
	return DiffSettings{Min: cfg.Min, Max: cfg.Max, TargetSeconds: cfg.TargetShare.Seconds(), Overrides: map[string]float64{}}
}

func TestDiffSettingsValidate(t *testing.T) {
	ok := DiffSettings{Min: 1, Max: 1e12, TargetSeconds: 10, Overrides: map[string]float64{"rig.1": 512}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*DiffSettings){
		"min > max":       func(d *DiffSettings) { d.Min, d.Max = 100, 10 },
		"min zero":        func(d *DiffSettings) { d.Min = 0 },
		"max too big":     func(d *DiffSettings) { d.Max = 1e16 },
		"max NaN":         func(d *DiffSettings) { d.Max = math.NaN() },
		"target zero":     func(d *DiffSettings) { d.TargetSeconds = 0 },
		"target too long": func(d *DiffSettings) { d.TargetSeconds = 601 },
		"fixed below min": func(d *DiffSettings) { d.FixedDiff = 0.5 },
		"fixed above max": func(d *DiffSettings) { d.FixedDiff = 2e12 },
		"fixed negative":  func(d *DiffSettings) { d.FixedDiff = -1 },
		"override zero":   func(d *DiffSettings) { d.Overrides["x"] = 0 },
		"override noname": func(d *DiffSettings) { d.Overrides[" "] = 5 },
		"override inf":    func(d *DiffSettings) { d.Overrides["x"] = math.Inf(1) },
	}
	for name, mutate := range bad {
		d := ok.Clone()
		mutate(&d)
		if d.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// authorizeSession runs mining.subscribe + mining.authorize on a pipe session.
func authorizeSession(t *testing.T, s *Server, user, pass string) *Session {
	t.Helper()
	sess, r, _ := pipeSession(t, s)
	go func() { _, _ = io.Copy(io.Discard, r) }()
	sess.handleLine([]byte(`{"id":1,"method":"mining.subscribe","params":[]}`))
	b, _ := json.Marshal(map[string]any{"id": 2, "method": "mining.authorize", "params": []string{user, pass}})
	sess.handleLine(b)
	return sess
}

func diffOf(s *Session) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.diff
}

// fastShares feeds accepted shares far faster than the target rate, which
// would make vardiff raise the difficulty if it were active.
func fastShares(s *Session, n int) {
	for i := 0; i < n; i++ {
		s.accept(json.RawMessage("9"), "", s.primary, diffOf(s), diffOf(s), "none", [32]byte{})
	}
}

func TestPasswordDifficultyOverride(t *testing.T) {
	cfg := testConfig() // vardiff min 1, max 1e12
	s := testServer(t, cfg)
	sess := authorizeSession(t, s, "rig.1", "d=512")
	if d := diffOf(sess); d != 512 {
		t.Fatalf("d=512 gave difficulty %v", d)
	}
	fastShares(sess, 50)
	if d := diffOf(sess); d != 512 {
		t.Fatalf("vardiff changed a password-fixed difficulty to %v", d)
	}
	if d := diffOf(authorizeSession(t, s, "rig.2", "x,d=5e20")); d != 1e12 {
		t.Fatalf("d above max not clamped: %v", d)
	}
	if d := diffOf(authorizeSession(t, s, "rig.3", "d=0.0001")); d != 1 {
		t.Fatalf("d below min not clamped: %v", d)
	}
	// Without d=, vardiff is active and reacts to the fast shares.
	free := authorizeSession(t, s, "rig.4", "x")
	before := diffOf(free)
	fastShares(free, 50)
	if diffOf(free) <= before {
		t.Fatalf("vardiff did not raise difficulty: %v -> %v", before, diffOf(free))
	}
}

func TestFixedDiffDisablesVardiff(t *testing.T) {
	cfg := testConfig()
	cfg.Vardiff.FixedDiff = 2048
	s := testServer(t, cfg)
	sess := authorizeSession(t, s, "rig.1", "x")
	if d := diffOf(sess); d != 2048 {
		t.Fatalf("fixed diff not applied: %v", d)
	}
	fastShares(sess, 50)
	sess.tick(time.Now().Add(time.Hour))
	if d := diffOf(sess); d != 2048 {
		t.Fatalf("fixed diff changed to %v", d)
	}
	// A miner's own d= still wins over the global fixed difficulty.
	if d := diffOf(authorizeSession(t, s, "rig.2", "d=64")); d != 64 {
		t.Fatalf("password override with FIXED_DIFF: %v", d)
	}
}

func TestLiveSettingsReapply(t *testing.T) {
	s := testServer(t, testConfig())
	a := authorizeSession(t, s, "rig.a", "x")
	b := authorizeSession(t, s, "rig.b", "d=300")
	c := authorizeSession(t, s, "rig.c", "x")
	ds := s.DiffSettings()
	ds.FixedDiff = 1000
	ds.Overrides["rig.c"] = 77
	if err := s.SetDiffSettings(ds); err != nil {
		t.Fatal(err)
	}
	if diffOf(a) != 1000 || diffOf(b) != 300 || diffOf(c) != 77 {
		t.Fatalf("after live update: a=%v b=%v c=%v", diffOf(a), diffOf(b), diffOf(c))
	}
	// Narrowing min/max re-clamps overrides and password difficulties.
	ds.FixedDiff = 0
	ds.Min, ds.Max = 400, 500
	if err := s.SetDiffSettings(ds); err != nil {
		t.Fatal(err)
	}
	if diffOf(a) != 500 || diffOf(b) != 400 || diffOf(c) != 400 {
		t.Fatalf("after re-clamp: a=%v b=%v c=%v", diffOf(a), diffOf(b), diffOf(c))
	}
	// Invalid settings are refused and leave the live ones untouched.
	bad := s.DiffSettings()
	bad.Min, bad.Max = 10, 1
	if s.SetDiffSettings(bad) == nil || s.DiffSettings().Min != 400 {
		t.Fatal("invalid settings applied")
	}
}

func TestParsePasswordRace(t *testing.T) {
	for pass, want := range map[string]string{
		"x,race=elf": "elf", "race=Dark-Elf": "darkelf", "d=8;race=half_orc": "halforc",
		"x race=gnome": "gnome", "x": "", "race=dragon": "", "race=halfling,d=2": "halfling",
	} {
		got, ok := parsePasswordRace(pass)
		if got != want || ok != (want != "") {
			t.Errorf("%q: got %q %v, want %q", pass, got, ok, want)
		}
	}
}

// Regression: an HTTP request (or any non-JSON traffic) on the Stratum port
// was counted as pool-level "malformed" share rejects that no worker owned.
// It is not a share: it is counted as a bad message and logged raw. A real
// submit that cannot be parsed is charged to the connection's worker.
func TestNonStratumTrafficIsNotAShareReject(t *testing.T) {
	s := testServer(t, testConfig())
	var logs strings.Builder
	s.log = slog.New(slog.NewTextHandler(&lockedWriter{w: &logs}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sess, r, _ := pipeSession(t, s)
	go io.Copy(io.Discard, r)

	httpReq := []string{"GET / HTTP/1.1", "Host: umbrel.local:51492", "User-Agent: Mozilla/5.0", "Accept: */*"}
	for _, l := range httpReq {
		sess.handleLine([]byte(l))
	}
	snap := s.st.Snapshot()
	if snap.Pool.Rejected != 0 || len(snap.Pool.Rejects) != 0 {
		t.Fatalf("HTTP lines counted as share rejects: %d %v", snap.Pool.Rejected, snap.Pool.Rejects)
	}
	if snap.Pool.BadMessages != uint64(len(httpReq)) {
		t.Fatalf("bad messages %d, want %d", snap.Pool.BadMessages, len(httpReq))
	}
	if !strings.Contains(logs.String(), `GET / HTTP/1.1`) {
		t.Fatalf("raw line not logged at debug:\n%s", logs.String())
	}

	// An authorized miner whose submit params cannot be read: its own reject.
	sess.handleLine([]byte(`{"id":1,"method":"mining.subscribe","params":[]}`))
	sess.handleLine([]byte(`{"id":2,"method":"mining.authorize","params":["rig.1","x"]}`))
	sess.handleLine([]byte(`{"id":3,"method":"mining.submit","params":["rig.1","1","0000000000000000",1700000000,"00000000"]}`))
	snap = s.st.Snapshot()
	var w *stats.WorkerSnapshot
	for i := range snap.Workers {
		if snap.Workers[i].Name == "rig.1" {
			w = &snap.Workers[i]
		}
	}
	if w == nil || w.Rejected != 1 || w.Rejects["malformed"] != 1 {
		t.Fatalf("unreadable submit not charged to the worker: %+v", w)
	}
	if snap.Pool.Rejected != 1 || snap.Pool.Rejects["malformed"] != 1 {
		t.Fatalf("pool rejects %d %v", snap.Pool.Rejected, snap.Pool.Rejects)
	}
	if !strings.Contains(logs.String(), `unreadable mining.submit`) || !strings.Contains(logs.String(), `1700000000`) {
		t.Fatalf("raw submit not logged:\n%s", logs.String())
	}
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
