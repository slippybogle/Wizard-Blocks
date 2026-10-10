package stratum

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/bitcoin"
	"github.com/fladnagmai/wizard-blocks/internal/work"
)

// Stratum error codes (de-facto standard from the original Stratum spec).
const (
	ErrOther         = 20
	ErrJobNotFound   = 21 // also used for stale shares
	ErrDuplicate     = 22
	ErrLowDifficulty = 23
	ErrUnauthorized  = 24
	ErrNotSubscribed = 25
)

// maxFutureTime mirrors the node's MAX_FUTURE_BLOCK_TIME (2 h) with margin
// for clock skew between us and the node.
const maxFutureTime = 7000

const (
	maxSessionJobs       = 256
	maxWorkersPerSession = 32
)

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type stratumError struct {
	code int
	msg  string
}

func (e *stratumError) Error() string { return e.msg }

func serr(code int, msg string) *stratumError { return &stratumError{code, msg} }

type sessJob struct {
	job  *work.Job
	diff float64 // share difficulty in force when the job was sent
}

// Session is one miner connection.
type Session struct {
	srv  *Server
	conn net.Conn
	ip   string
	en1  uint32

	en1Bytes []byte
	en1Hex   string

	out       chan []byte
	closeOnce sync.Once
	done      chan struct{}

	// Token bucket for inbound message rate limiting (reader goroutine only).
	tokens     float64
	tokensAt   time.Time
	badMsgs    int
	authorized bool // reader goroutine copy, also under mu

	mu          sync.Mutex
	subscribed  bool
	workers     map[string]bool
	primary     string
	payout      *Payout
	versionMask uint32
	diff        float64 // current vardiff difficulty (before network cap)
	sentDiff    float64 // last value sent via mining.set_difficulty
	started     bool    // initial difficulty + job sent
	jobs        map[string]sessJob
	jobOrder    []string
	vd          *vardiff
	pwDiff      float64 // miner's own "d=" password difficulty (0 = none)
	firstLine   []byte  // the connection's first line, for logging non-stratum clients
	lastWork    *work.Work
}

func newSession(s *Server, conn net.Conn, ip string, en1 uint32) *Session {
	b := binary.BigEndian.AppendUint32(nil, en1)
	return &Session{
		srv: s, conn: conn, ip: ip, en1: en1, en1Bytes: b, en1Hex: hex.EncodeToString(b),
		out: make(chan []byte, 64), done: make(chan struct{}),
		tokens: s.cfg.MsgBurst, tokensAt: time.Now(),
		workers: map[string]bool{}, jobs: map[string]sessJob{},
		diff: s.diffs.Load().Clamp(s.cfg.Vardiff.Initial),
		vd:   newVardiff(s.cfg.Vardiff, time.Now()),
	}
}

func (c *Session) close(reason string) {
	c.closeOnce.Do(func() {
		close(c.done)
		c.conn.Close()
		c.srv.log.Debug("session closed", "ip", c.ip, "en1", c.en1Hex, "reason", reason)
	})
}

// send queues a message without blocking; a client that cannot keep up with
// its own message stream is disconnected rather than allowed to stall us.
func (c *Session) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		c.srv.log.Error("marshal stratum message", "err", err)
		return
	}
	b = append(b, '\n')
	select {
	case c.out <- b:
	case <-c.done:
	default:
		c.close("slow client: send buffer full")
	}
}

func (c *Session) reply(id json.RawMessage, result any, e *stratumError) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	var errv any
	if e != nil {
		errv = []any{e.code, e.msg, nil}
	}
	c.send(map[string]any{"id": id, "result": result, "error": errv})
}

func (c *Session) notify(method string, params []any) {
	c.send(map[string]any{"id": nil, "method": method, "params": params})
}

func (c *Session) writer() {
	for {
		select {
		case <-c.done:
			return
		case b := <-c.out:
			_ = c.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, err := c.conn.Write(b); err != nil {
				c.close("write error: " + err.Error())
				return
			}
		}
	}
}

func (c *Session) run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			// Never let one connection take the engine down.
			c.srv.log.Error("panic in session (recovered)", "ip", c.ip, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
		}
		c.close("ended")
		c.mu.Lock()
		workers := make([]string, 0, len(c.workers))
		for w := range c.workers {
			workers = append(workers, w)
		}
		c.mu.Unlock()
		for _, w := range workers {
			c.srv.st.Disconnected(w)
		}
		if len(workers) > 0 {
			c.srv.log.Info("miner disconnected", "ip", c.ip, "workers", workers)
		}
	}()
	go c.writer()
	stop := context.AfterFunc(ctx, func() { c.close("shutdown") })
	defer stop()

	r := bufio.NewReaderSize(c.conn, c.srv.cfg.MaxLineBytes)
	for {
		timeout := c.srv.cfg.IdleTimeout
		if !c.authorized {
			timeout = c.srv.cfg.AuthTimeout
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
		line, err := r.ReadSlice('\n')
		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				c.close("line too long")
			} else {
				c.close("read: " + err.Error())
			}
			return
		}
		if !c.allow() {
			c.srv.log.Warn("rate limit exceeded; disconnecting", "ip", c.ip)
			c.close("rate limit")
			return
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if !c.handleLine(line) {
			return
		}
	}
}

func (c *Session) allow() bool {
	now := time.Now()
	c.tokens = math.Min(c.srv.cfg.MsgBurst, c.tokens+now.Sub(c.tokensAt).Seconds()*c.srv.cfg.MsgRate)
	c.tokensAt = now
	if c.tokens < 1 {
		return false
	}
	c.tokens--
	return true
}

// rawSample quotes the start of a raw message for the logs.
func rawSample(b []byte) string {
	if len(b) > 200 {
		return strconv.Quote(string(b[:200])) + "…"
	}
	return strconv.Quote(string(b))
}

// primaryWorker is the connection's first authorized worker, or "".
func (c *Session) primaryWorker() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.primary
}

// handleLine processes one JSON-RPC message; returns false to disconnect.
func (c *Session) handleLine(line []byte) bool {
	if c.firstLine == nil {
		c.firstLine = append([]byte(nil), line...)
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		// Not JSON-RPC at all: usually something that is not a miner talking
		// to the Stratum port (a browser or HTTP health check sends one line
		// per header). Not a share, so it is not counted as a share reject.
		c.badMsgs++
		c.srv.st.BadMessage()
		c.srv.log.Debug("non-JSON line on stratum port", "ip", c.ip, "worker", c.primaryWorker(), "err", err.Error(), "raw", rawSample(line))
		if c.badMsgs > 10 {
			c.srv.log.Info("disconnected non-stratum client", "ip", c.ip, "first_line", rawSample(c.firstLine))
			c.close("too many malformed messages")
			return false
		}
		c.reply(nil, nil, serr(ErrOther, "malformed json"))
		return true
	}
	if req.Method == "" {
		return true // a response to one of our notifications; nothing to do
	}
	switch req.Method {
	case "mining.subscribe":
		c.handleSubscribe(req)
	case "mining.authorize":
		c.handleAuthorize(req)
	case "mining.configure":
		c.handleConfigure(req)
	case "mining.submit":
		c.handleSubmit(req)
	case "mining.extranonce.subscribe":
		// Extranonce1 never changes for a connection, so there is nothing to
		// push later; acknowledging lets miners that insist on it proceed.
		c.reply(req.ID, true, nil)
	case "mining.suggest_difficulty":
		c.handleSuggestDifficulty(req)
	case "mining.ping":
		c.reply(req.ID, "pong", nil)
	default:
		if len(req.ID) > 0 && string(req.ID) != "null" {
			c.reply(req.ID, nil, serr(ErrOther, "unsupported method "+truncate(req.Method, 64)))
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (c *Session) handleSubscribe(req request) {
	c.mu.Lock()
	c.subscribed = true
	c.mu.Unlock()
	sid := c.en1Hex
	c.reply(req.ID, []any{
		[]any{[]any{"mining.set_difficulty", sid}, []any{"mining.notify", sid}},
		c.en1Hex,
		c.srv.cfg.Extranonce2Size,
	}, nil)
	c.maybeStart()
}

func parseStrings(raw json.RawMessage) ([]string, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, errors.New("params must be an array")
	}
	out := make([]string, len(arr))
	for i, a := range arr {
		if string(a) == "null" {
			continue
		}
		if err := json.Unmarshal(a, &out[i]); err != nil {
			return nil, fmt.Errorf("param %d must be a string", i)
		}
	}
	return out, nil
}

func (c *Session) handleAuthorize(req request) {
	ps, err := parseStrings(req.Params)
	if err != nil || len(ps) < 1 || ps[0] == "" || len(ps[0]) > 256 {
		c.reply(req.ID, false, serr(ErrUnauthorized, "invalid username"))
		return
	}
	user := ps[0]
	pass := ""
	if len(ps) > 1 {
		pass = ps[1]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	payout, err := c.srv.resolve(ctx, user)
	cancel()
	if err != nil {
		c.srv.log.Warn("authorize rejected", "ip", c.ip, "user", truncate(user, 128), "err", err)
		c.srv.st.ShareRejected("", "unauthorized")
		c.reply(req.ID, false, serr(ErrUnauthorized, "unauthorized: "+err.Error()))
		return
	}
	c.mu.Lock()
	if c.payout != nil && !bytes.Equal(c.payout.Script, payout.Script) {
		c.mu.Unlock()
		c.reply(req.ID, false, serr(ErrUnauthorized, "only one payout address per connection"))
		return
	}
	if !c.workers[user] && len(c.workers) >= maxWorkersPerSession {
		c.mu.Unlock()
		c.reply(req.ID, false, serr(ErrUnauthorized, "too many workers on one connection"))
		return
	}
	c.payout = payout
	isNew := !c.workers[user]
	c.workers[user] = true
	if c.primary == "" {
		c.primary = user
	}
	if d, ok := parsePasswordDiff(pass); ok {
		c.pwDiff = d
	}
	ds := c.srv.diffs.Load()
	if fd, ok := c.fixedLocked(ds); ok {
		c.diff = fd
	}
	started := c.started
	c.authorized = true
	c.mu.Unlock()
	if isNew {
		c.srv.st.Connected(user)
		if race, ok := parsePasswordRace(pass); ok {
			c.srv.st.SetRace(user, race)
		}
		c.srv.log.Info("miner authorized", "ip", c.ip, "user", user, "payout", payout.Address, "en1", c.en1Hex)
	}
	c.reply(req.ID, true, nil)
	if started {
		c.applyDiffChange()
	}
	c.maybeStart()
}

// fixedLocked returns the fixed difficulty for this connection, if any
// (see DiffSettings for the precedence). c.mu must be held.
func (c *Session) fixedLocked(ds *DiffSettings) (float64, bool) {
	if v, ok := ds.OverrideFor(sortedKeys(c.workers, c.primary)); ok {
		return v, true
	}
	if c.pwDiff > 0 {
		return ds.Clamp(c.pwDiff), true
	}
	if ds.FixedDiff > 0 {
		return ds.Clamp(ds.FixedDiff), true
	}
	return 0, false
}

// reapplyDiff re-evaluates the difficulty after a settings change.
func (c *Session) reapplyDiff() {
	ds := c.srv.diffs.Load()
	c.mu.Lock()
	if fd, ok := c.fixedLocked(ds); ok {
		c.diff = fd
	} else {
		c.diff = ds.Clamp(c.diff)
	}
	c.vd.reset(time.Now())
	started := c.started
	c.mu.Unlock()
	if started {
		c.applyDiffChange()
	}
}

// parsePasswordDiff recognises the common "d=1024" password convention.
func parsePasswordDiff(pass string) (float64, bool) {
	for _, f := range strings.FieldsFunc(pass, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if v, ok := strings.CutPrefix(f, "d="); ok {
			d, err := strconv.ParseFloat(v, 64)
			if err == nil && d > 0 && !math.IsInf(d, 0) {
				return d, true
			}
		}
	}
	return 0, false
}

// parsePasswordRace recognises "race=elf" in the password: the race of the
// miner's character in the UI. Returns the canonical race name.
func parsePasswordRace(pass string) (string, bool) {
	for _, f := range strings.FieldsFunc(pass, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		v, ok := strings.CutPrefix(strings.ToLower(f), "race=")
		if !ok {
			continue
		}
		v = strings.NewReplacer("-", "", "_", "", ".", "").Replace(v)
		switch v {
		case "human", "elf", "dwarf", "halfling", "gnome":
			return v, true
		case "darkelf", "drow":
			return "darkelf", true
		case "halforc", "orc":
			return "halforc", true
		}
	}
	return "", false
}

func (c *Session) handleConfigure(req request) {
	var params []json.RawMessage
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		c.reply(req.ID, nil, serr(ErrOther, "invalid configure params"))
		return
	}
	var exts []string
	if err := json.Unmarshal(params[0], &exts); err != nil {
		c.reply(req.ID, nil, serr(ErrOther, "invalid extension list"))
		return
	}
	opts := map[string]json.RawMessage{}
	if len(params) > 1 {
		_ = json.Unmarshal(params[1], &opts)
	}
	res := map[string]any{}
	for _, ext := range exts {
		switch ext {
		case "version-rolling":
			// BIP310: negotiated mask = miner mask ∧ server mask.
			minerMask := uint32(0xffffffff)
			if raw, ok := opts["version-rolling.mask"]; ok {
				var ms string
				if json.Unmarshal(raw, &ms) == nil {
					if v, err := strconv.ParseUint(ms, 16, 32); err == nil {
						minerMask = uint32(v)
					}
				}
			}
			mask := minerMask & c.srv.cfg.VersionRollingMask
			c.mu.Lock()
			c.versionMask = mask
			c.mu.Unlock()
			res["version-rolling"] = mask != 0
			res["version-rolling.mask"] = fmt.Sprintf("%08x", mask)
		default:
			res[truncate(ext, 64)] = false
		}
	}
	c.reply(req.ID, res, nil)
}

func (c *Session) handleSuggestDifficulty(req request) {
	var ps []float64
	if err := json.Unmarshal(req.Params, &ps); err != nil || len(ps) < 1 || !(ps[0] > 0) {
		if len(req.ID) > 0 && string(req.ID) != "null" {
			c.reply(req.ID, nil, serr(ErrOther, "invalid difficulty"))
		}
		return
	}
	c.mu.Lock()
	ds := c.srv.diffs.Load()
	if _, fixed := c.fixedLocked(ds); !fixed {
		c.diff = ds.Clamp(ps[0]) // a suggestion: vardiff continues from here
	}
	started := c.started
	c.mu.Unlock()
	if len(req.ID) > 0 && string(req.ID) != "null" {
		c.reply(req.ID, true, nil)
	}
	if started {
		c.applyDiffChange()
	}
}

// maybeStart sends the initial difficulty and job once subscribed+authorized.
func (c *Session) maybeStart() {
	c.mu.Lock()
	ready := c.subscribed && c.payout != nil && !c.started
	c.mu.Unlock()
	if !ready {
		return
	}
	if w := c.srv.mgr.Current(); w != nil {
		c.sendWork(w)
	}
}

// effectiveDiff caps the share difficulty at the network difficulty so that
// every block-solving hash is also a valid share the miner will submit.
func effectiveDiff(d float64, w *work.Work) float64 {
	if nd := w.Tmpl.NetworkDiff; nd > 0 && d > nd {
		d = nd
	}
	if w.Aux != nil && w.Aux.MinDiff > 0 && d > w.Aux.MinDiff {
		d = w.Aux.MinDiff
	}
	return d
}

// sendWork sends w (as a job for this session's payout) if the session is ready.
func (c *Session) sendWork(w *work.Work) {
	c.mu.Lock()
	if !c.subscribed || c.payout == nil {
		c.mu.Unlock()
		return
	}
	job, err := w.Job(c.payout.Script, c.payout.Address)
	if err != nil {
		c.mu.Unlock()
		c.srv.log.Error("cannot build job", "err", err, "payout", c.payout.Address)
		return
	}
	clean := w.Clean || !c.started
	if c.lastWork != nil && c.lastWork.Gen != w.Gen {
		clean = true
	}
	c.started = true
	c.lastWork = w
	d := effectiveDiff(c.diff, w)
	sendDiff := d != c.sentDiff
	c.sentDiff = d
	c.recordJobLocked(job, d, clean)
	worker := c.primary
	c.mu.Unlock()

	if sendDiff {
		c.notify("mining.set_difficulty", []any{d})
		c.srv.st.SetDifficulty(worker, d)
	}
	c.notify("mining.notify", job.NotifyParams(clean))
}

func (c *Session) recordJobLocked(job *work.Job, d float64, clean bool) {
	if clean {
		// Keep jobs of the previous generation only to classify late shares as stale.
		gen := job.Gen
		kept := c.jobOrder[:0]
		for _, id := range c.jobOrder {
			if sj, ok := c.jobs[id]; ok && sj.job.Gen+1 >= gen {
				kept = append(kept, id)
			} else {
				delete(c.jobs, id)
			}
		}
		c.jobOrder = kept
	}
	c.jobs[job.ID] = sessJob{job: job, diff: d}
	c.jobOrder = append(c.jobOrder, job.ID)
	for len(c.jobOrder) > maxSessionJobs {
		delete(c.jobs, c.jobOrder[0])
		c.jobOrder = c.jobOrder[1:]
	}
}

// applyDiffChange announces the current difficulty and re-sends the current
// work under a new job id so it unambiguously applies.
func (c *Session) applyDiffChange() {
	c.mu.Lock()
	w := c.lastWork
	if w == nil || c.payout == nil {
		c.mu.Unlock()
		return
	}
	cur, err := w.Job(c.payout.Script, c.payout.Address)
	if err != nil {
		c.mu.Unlock()
		return
	}
	d := effectiveDiff(c.diff, w)
	if d == c.sentDiff {
		c.mu.Unlock()
		return
	}
	job := c.srv.mgr.Resend(cur)
	c.sentDiff = d
	c.recordJobLocked(job, d, false)
	worker := c.primary
	c.mu.Unlock()
	c.notify("mining.set_difficulty", []any{d})
	c.notify("mining.notify", job.NotifyParams(false))
	c.srv.st.SetDifficulty(worker, d)
	c.srv.log.Debug("difficulty changed", "ip", c.ip, "worker", worker, "diff", d)
}

func (c *Session) tick(now time.Time) {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return
	}
	ds := c.srv.diffs.Load()
	if _, fixed := c.fixedLocked(ds); fixed {
		c.mu.Unlock()
		return
	}
	nd, changed := c.vd.onTick(now, c.diff, *ds)
	if changed {
		c.diff = nd
	}
	c.mu.Unlock()
	if changed {
		c.applyDiffChange()
	}
}

func parseHex32(s string) (uint32, bool) {
	if len(s) != 8 {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	return uint32(v), err == nil
}

func (c *Session) reject(id json.RawMessage, worker, reason string, e *stratumError) {
	c.srv.st.ShareRejected(worker, reason)
	c.srv.log.Debug("share rejected", "ip", c.ip, "worker", worker, "reason", reason)
	c.reply(id, false, e)
}

func (c *Session) handleSubmit(req request) {
	ps, err := parseStrings(req.Params)
	if err != nil || len(ps) < 5 {
		// A real submit we cannot read: charge it to this connection's worker.
		worker := c.primaryWorker()
		c.srv.log.Debug("unreadable mining.submit", "ip", c.ip, "worker", worker, "raw", rawSample(req.Params))
		c.reject(req.ID, worker, "malformed", serr(ErrOther, "invalid submit params"))
		return
	}
	worker, jobID, en2Hex, ntimeHex, nonceHex := ps[0], ps[1], ps[2], ps[3], ps[4]

	c.mu.Lock()
	subscribed, authorized := c.subscribed, c.workers[worker]
	sj, haveJob := c.jobs[jobID]
	mask := c.versionMask
	curDiff := effectiveDiffOrRaw(c.diff, c.lastWork)
	c.mu.Unlock()

	if !subscribed {
		c.reject(req.ID, "", "not-subscribed", serr(ErrNotSubscribed, "not subscribed"))
		return
	}
	if !authorized {
		c.reject(req.ID, "", "unauthorized", serr(ErrUnauthorized, "unauthorized worker"))
		return
	}
	if !haveJob {
		c.reject(req.ID, worker, "job-not-found", serr(ErrJobNotFound, "job not found"))
		return
	}
	job := sj.job
	en2, err := hex.DecodeString(en2Hex)
	if err != nil || len(en2) != c.srv.cfg.Extranonce2Size {
		c.srv.log.Debug("malformed mining.submit", "ip", c.ip, "worker", worker, "raw", rawSample(req.Params))
		c.reject(req.ID, worker, "malformed", serr(ErrOther, "invalid extranonce2"))
		return
	}
	ntime, ok1 := parseHex32(ntimeHex)
	nonce, ok2 := parseHex32(nonceHex)
	if !ok1 || !ok2 {
		c.srv.log.Debug("malformed mining.submit", "ip", c.ip, "worker", worker, "raw", rawSample(req.Params))
		c.reject(req.ID, worker, "malformed", serr(ErrOther, "invalid ntime or nonce"))
		return
	}

	// Version rolling: see version.go for the interpretations in use.
	jv := job.Tmpl.Version
	cands := []versionCandidate{{jv, InterpNone}}
	if len(ps) >= 6 && ps[5] != "" {
		bits, ok := parseHex32(ps[5])
		if !ok {
			c.srv.log.Debug("malformed mining.submit", "ip", c.ip, "worker", worker, "raw", rawSample(req.Params))
			c.reject(req.ID, worker, "malformed", serr(ErrOther, "invalid version bits"))
			return
		}
		if bits&^mask != 0 {
			c.reject(req.ID, worker, "invalid-version", serr(ErrOther, "version bits outside negotiated mask"))
			return
		}
		cands = versionCandidates(jv, mask, bits)
	}

	now := time.Now().Unix()
	if ntime < job.Tmpl.MinShareTime || int64(ntime) > now+maxFutureTime {
		c.reject(req.ID, worker, "invalid-ntime", serr(ErrOther, "ntime out of range"))
		return
	}

	required := math.Min(sj.diff, curDiff)
	algo := c.srv.mgr.PoW()
	shareTarget := algo.ShareTarget(required)
	powHash := func(h bitcoin.Header) bitcoin.Hash { b := h.Serialize(); return algo.PoWHash(b[:]) }

	// Evaluate every admissible interpretation and keep the lowest hash. On
	// mainnet at most one can meet a real target (and they coincide unless the
	// template signals inside the mask); on test chains with trivial targets
	// this picks the header the miner actually worked on.
	hdr := job.Header(c.en1Bytes, en2, ntime, nonce, cands[0].version)
	hash := powHash(hdr) // the work hash (SHA-256d, or Scrypt for LTC); the block's id stays SHA-256d
	interp := cands[0].interp
	for _, vc := range cands[1:] {
		h := job.Header(c.en1Bytes, en2, ntime, nonce, vc.version)
		if hh := powHash(h); bitcoin.HashToBig(hh).Cmp(bitcoin.HashToBig(hash)) < 0 {
			hdr, hash, interp = h, hh, vc.interp
		}
	}
	achieved := algo.HashDifficulty(hash)
	stale := job.Gen < c.srv.mgr.CurrentGen()

	// Merged mining: the same header may solve an aux block (Dogecoin),
	// whatever it means for the parent; the aux node judges whether its
	// block is still current.
	if job.Aux != nil {
		c.srv.mgr.SubmitAux(work.Candidate{Job: job, Header: hdr, En1: c.en1Bytes, En2: en2,
			Worker: worker, ShareDiff: achieved, Stale: stale, VersionInterp: interp}, hash)
	}

	// Block check first and independently of the share target: a solved
	// block is submitted immediately, before any bookkeeping.
	if bitcoin.HashMeetsTarget(hash, job.Tmpl.Target) {
		c.srv.mgr.SubmitBlock(work.Candidate{
			Job: job, Header: hdr, En1: c.en1Bytes, En2: en2,
			Worker: worker, ShareDiff: achieved, Stale: stale, VersionInterp: interp,
		})
		if !stale {
			if c.srv.seen(job.Gen, hash) {
				c.reject(req.ID, worker, "duplicate", serr(ErrDuplicate, "duplicate share"))
				return
			}
			c.accept(req.ID, job.Tmpl.PrevHash.String(), worker, required, achieved, interp, hash)
			return
		}
	}
	if stale {
		c.reject(req.ID, worker, "stale", serr(ErrJobNotFound, "stale share (job superseded by new block)"))
		return
	}
	if !bitcoin.HashMeetsTarget(hash, shareTarget) {
		c.reject(req.ID, worker, "low-difficulty", serr(ErrLowDifficulty, fmt.Sprintf("low difficulty share (%.6g < %.6g)", achieved, required)))
		return
	}
	if c.srv.seen(job.Gen, hash) {
		c.reject(req.ID, worker, "duplicate", serr(ErrDuplicate, "duplicate share"))
		return
	}
	c.accept(req.ID, job.Tmpl.PrevHash.String(), worker, required, achieved, interp, hash)
}

func effectiveDiffOrRaw(d float64, w *work.Work) float64 {
	if w == nil {
		return d
	}
	return effectiveDiff(d, w)
}

// accept answers a valid share; prevHash is the previous-block hash of the
// job it was mined on, so stats credit the right round.
func (c *Session) accept(id json.RawMessage, prevHash, worker string, credited, achieved float64, interp string, hash bitcoin.Hash) {
	c.reply(id, true, nil)
	c.srv.st.ShareAcceptedOn(prevHash, worker, credited, achieved, interp)
	c.srv.log.Debug("share accepted", "ip", c.ip, "worker", worker, "hash", hash.String(),
		"difficulty", achieved, "version_interp", interp)
	c.mu.Lock()
	ds := c.srv.diffs.Load()
	nd, changed := c.diff, false
	if _, fixed := c.fixedLocked(ds); !fixed {
		nd, changed = c.vd.onShare(time.Now(), credited, c.diff, *ds)
	}
	if changed {
		c.diff = nd
	}
	c.mu.Unlock()
	if changed {
		c.applyDiffChange()
	}
}
