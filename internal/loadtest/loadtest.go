// Package loadtest drives many simulated rental-style Stratum connections
// against a Wizard-Blocks engine to find its limits. It is meant for
// loopback only: it never hashes for real work, it submits shares that meet
// the pool's (tiny) test difficulty but miss the network target, so the
// engine does its full share validation without finding blocks.
package loadtest

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"net"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/pow"
)

// Profile is the handshake a simulated rig performs.
type Profile string

const (
	// NiceHash: mining.configure (version rolling, SHA256AsicBoost only),
	// subscribe as "NiceHash/1.0.0", mining.extranonce.subscribe (NiceHash
	// spec), authorize "x"; shares carry rolled version bits. NiceHash's
	// published pool requirements: extranonce2 size >= 4 and pool
	// difficulty >= 8 (SHA256AsicBoost, Scrypt).
	NiceHash Profile = "nicehash"
	// MRR (MiningRigRentals, pool URL with #xnsub): subscribe,
	// mining.extranonce.subscribe, authorize with "x" or "x,d=<n>" (PwDiff);
	// suggest_difficulty when Suggest is set. Rigs rented under one pool
	// profile share one username: with SameName every connection
	// authorizes the same worker.
	MRR Profile = "mrr"
	// Mixed alternates NiceHash and MRR connections.
	Mixed Profile = "mixed"
)

// Config configures a run.
type Config struct {
	Addr      string   // engine stratum address (loopback)
	SourceIPs []string // local addresses to dial from (127.0.0.x); empty = default
	Conns     int
	Ramp      time.Duration // spread connection opening over this long
	Profile   Profile
	Scrypt    bool   // Litecoin-style shares
	NameFmt   string // worker name format with one %d, e.g. "rentx.rig%05d"
	PwDiff    float64
	Suggest   float64
	SameName  bool // every connection authorizes the same worker (MRR pool profile)
	// ShareRate is shares per second per connection (0 = idle connections).
	ShareRate float64
	Duration  time.Duration // how long to submit after all connections are up
	Timeout   time.Duration // per request
}

// Result is what a run measured.
type Result struct {
	Opened, Refused, Failed    int
	HandshakeP50, HandshakeP99 time.Duration
	Submitted, Accepted        uint64
	Rejected                   map[string]uint64 // by error text
	SubmitP50, SubmitP99       time.Duration
	SubmitMax                  time.Duration
	Disconnected               int // connections closed by the server during the run
	Elapsed                    time.Duration
	Difficulties               map[string]int // difficulty at the end of the run, per connection
	FailReasons                map[string]int
	MinExtranonce2Size         int    // smallest extranonce2 size the pool gave (NiceHash needs >= 4)
	VersionRolled              uint64 // accepted shares that carried rolled version bits
}

// AcceptedPerSec is accepted shares per second over the submit phase.
func (r *Result) AcceptedPerSec() float64 {
	if r.Elapsed <= 0 {
		return 0
	}
	return float64(r.Accepted) / r.Elapsed.Seconds()
}

func (r *Result) String() string {
	rej := uint64(0)
	for _, n := range r.Rejected {
		rej += n
	}
	return fmt.Sprintf("opened=%d refused=%d failed=%d handshake p50=%v p99=%v en2=%d | submitted=%d accepted=%d (%.0f/s, %d version-rolled) rejected=%d %v | submit p50=%v p99=%v max=%v | disconnected=%d diffs=%v fails=%v",
		r.Opened, r.Refused, r.Failed, r.HandshakeP50.Round(time.Microsecond), r.HandshakeP99.Round(time.Microsecond), r.MinExtranonce2Size,
		r.Submitted, r.Accepted, r.AcceptedPerSec(), r.VersionRolled, rej, r.Rejected,
		r.SubmitP50.Round(time.Microsecond), r.SubmitP99.Round(time.Microsecond), r.SubmitMax.Round(time.Microsecond),
		r.Disconnected, r.Difficulties, r.FailReasons)
}

// job is a parsed mining.notify.
type job struct {
	id       string
	prevHash []byte
	coinb1   []byte
	coinb2   []byte
	branch   [][]byte
	version  uint32
	bits     uint32
	ntime    uint32
}

type response struct {
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func (r *response) ok() bool {
	return string(r.Result) == "true" && (len(r.Error) == 0 || string(r.Error) == "null")
}

func (r *response) errText() string {
	var e []any
	if json.Unmarshal(r.Error, &e) == nil && len(e) > 1 {
		return fmt.Sprint(e[1])
	}
	if len(r.Error) == 0 || string(r.Error) == "null" {
		return "result " + string(r.Result)
	}
	return string(r.Error)
}

// conn is a lightweight Stratum client (small buffers: thousands per run).
type conn struct {
	c   net.Conn
	wmu sync.Mutex
	id  atomic.Uint64

	pmu     sync.Mutex
	pending map[uint64]chan *response

	mu      sync.Mutex
	en1     []byte
	en2Size int
	diff    float64
	mask    uint32 // negotiated version-rolling mask (NiceHash on SHA-256d)
	job     *job
	gotJob  chan struct{}
	jobOnce sync.Once

	closed   chan struct{}
	answered atomic.Bool // the server answered at least one request
}

func dial(ctx context.Context, src, addr string) (*conn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	if src != "" {
		d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(src)}
	}
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c := &conn{c: nc, pending: map[uint64]chan *response{}, gotJob: make(chan struct{}), closed: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

func (c *conn) readLoop() {
	defer close(c.closed)
	r := bufio.NewReaderSize(c.c, 4096)
	for {
		line, err := r.ReadSlice('\n')
		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				// Long notify (big merkle branch): read the rest.
				rest, err2 := r.ReadBytes('\n')
				if err2 != nil {
					return
				}
				line = append(append([]byte(nil), line...), rest...)
			} else {
				return
			}
		}
		var m struct {
			ID     *uint64           `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
			Result json.RawMessage   `json:"result"`
			Error  json.RawMessage   `json:"error"`
		}
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if m.Method != "" {
			c.notify(m.Method, m.Params)
			continue
		}
		if m.ID == nil {
			continue
		}
		c.pmu.Lock()
		ch := c.pending[*m.ID]
		delete(c.pending, *m.ID)
		c.pmu.Unlock()
		c.answered.Store(true)
		if ch != nil {
			ch <- &response{Result: append(json.RawMessage(nil), m.Result...), Error: append(json.RawMessage(nil), m.Error...)}
		}
	}
}

func (c *conn) notify(method string, p []json.RawMessage) {
	switch method {
	case "mining.set_difficulty":
		var d float64
		if len(p) > 0 && json.Unmarshal(p[0], &d) == nil {
			c.mu.Lock()
			c.diff = d
			c.mu.Unlock()
		}
	case "mining.set_extranonce":
		// NiceHash extension: new extranonce1 / extranonce2 size, used from
		// the next job on (this pool never sends it; handled for fidelity).
		var en1 string
		var size int
		if len(p) >= 2 && json.Unmarshal(p[0], &en1) == nil && json.Unmarshal(p[1], &size) == nil {
			if b, err := hex.DecodeString(en1); err == nil {
				c.mu.Lock()
				c.en1, c.en2Size = b, size
				c.mu.Unlock()
			}
		}
	case "mining.notify":
		j, err := parseNotify(p)
		if err != nil {
			return
		}
		c.mu.Lock()
		c.job = j
		c.mu.Unlock()
		c.jobOnce.Do(func() { close(c.gotJob) })
	}
}

func parseNotify(p []json.RawMessage) (*job, error) {
	if len(p) < 9 {
		return nil, errors.New("short notify")
	}
	var s [8]string
	for i, k := range []int{0, 1, 2, 3, 5, 6, 7} {
		if json.Unmarshal(p[k], &s[i]) != nil {
			return nil, errors.New("notify field")
		}
	}
	var br []string
	if json.Unmarshal(p[4], &br) != nil {
		return nil, errors.New("notify branch")
	}
	j := &job{id: s[0]}
	ph, err := hex.DecodeString(s[1])
	if err != nil || len(ph) != 32 {
		return nil, errors.New("prevhash")
	}
	// Stratum sends the previous hash with each 4-byte word swapped.
	for i := 0; i < 32; i += 4 {
		ph[i], ph[i+1], ph[i+2], ph[i+3] = ph[i+3], ph[i+2], ph[i+1], ph[i]
	}
	j.prevHash = ph
	if j.coinb1, err = hex.DecodeString(s[2]); err != nil {
		return nil, err
	}
	if j.coinb2, err = hex.DecodeString(s[3]); err != nil {
		return nil, err
	}
	for _, b := range br {
		x, err := hex.DecodeString(b)
		if err != nil {
			return nil, err
		}
		j.branch = append(j.branch, x)
	}
	for i, dst := range []*uint32{&j.version, &j.bits, &j.ntime} {
		v, err := strconv.ParseUint(s[4+i], 16, 32)
		if err != nil {
			return nil, err
		}
		*dst = uint32(v)
	}
	return j, nil
}

func (c *conn) call(ctx context.Context, timeout time.Duration, method string, params ...any) (*response, error) {
	id := c.id.Add(1)
	if params == nil {
		params = []any{}
	}
	b, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	ch := make(chan *response, 1)
	c.pmu.Lock()
	c.pending[id] = ch
	c.pmu.Unlock()
	c.wmu.Lock()
	c.c.SetWriteDeadline(time.Now().Add(timeout))
	_, err = c.c.Write(append(b, '\n'))
	c.wmu.Unlock()
	if err != nil {
		return nil, err
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case r := <-ch:
		return r, nil
	case <-c.closed:
		return nil, errors.New("connection closed by server")
	case <-t.C:
		c.pmu.Lock()
		delete(c.pending, id)
		c.pmu.Unlock()
		return nil, fmt.Errorf("%s: no answer in %v", method, timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// handshake runs the profile's opening sequence and waits for the first job.
func (c *conn) handshake(ctx context.Context, cfg *Config, p Profile, worker string) error {
	to := cfg.Timeout
	pass := "x"
	if p == NiceHash && !cfg.Scrypt {
		r, err := c.call(ctx, to, "mining.configure", []string{"version-rolling"},
			map[string]any{"version-rolling.mask": "1fffe000", "version-rolling.min-bit-count": 2})
		if err != nil {
			return err
		}
		var res map[string]any
		if json.Unmarshal(r.Result, &res) == nil && res["version-rolling"] == true {
			if ms, ok := res["version-rolling.mask"].(string); ok {
				if m, err := strconv.ParseUint(ms, 16, 32); err == nil {
					c.mu.Lock()
					c.mask = uint32(m)
					c.mu.Unlock()
				}
			}
		}
	}
	agent := "NiceHash/1.0.0"
	if p == MRR {
		agent = "MRR/1.0"
		if cfg.PwDiff > 0 {
			pass = "x,d=" + strconv.FormatFloat(cfg.PwDiff, 'f', -1, 64)
		}
	}
	r, err := c.call(ctx, to, "mining.subscribe", agent)
	if err != nil {
		return err
	}
	var res []json.RawMessage
	if json.Unmarshal(r.Result, &res) != nil || len(res) < 3 {
		return fmt.Errorf("subscribe: %s", r.errText())
	}
	var en1 string
	var size int
	if json.Unmarshal(res[1], &en1) != nil || json.Unmarshal(res[2], &size) != nil {
		return errors.New("subscribe fields")
	}
	b, err := hex.DecodeString(en1)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.en1, c.en2Size = b, size
	c.mu.Unlock()
	if r, err := c.call(ctx, to, "mining.extranonce.subscribe"); err != nil || !r.ok() {
		if err == nil {
			err = fmt.Errorf("extranonce.subscribe: %s", r.errText())
		}
		return err
	}
	if r, err := c.call(ctx, to, "mining.authorize", worker, pass); err != nil || !r.ok() {
		if err == nil {
			err = fmt.Errorf("authorize: %s", r.errText())
		}
		return err
	}
	if p == MRR && cfg.Suggest > 0 {
		if r, err := c.call(ctx, to, "mining.suggest_difficulty", cfg.Suggest); err != nil || !r.ok() {
			if err == nil {
				err = fmt.Errorf("suggest_difficulty: %s", r.errText())
			}
			return err
		}
	}
	select {
	case <-c.gotJob:
		return nil
	case <-c.closed:
		return errors.New("connection closed by server")
	case <-time.After(to):
		return errors.New("no job after handshake")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func sha256d(b []byte) [32]byte {
	h := sha256.Sum256(b)
	return sha256.Sum256(h[:])
}

func header(j *job, en1, en2 []byte, version, nonce uint32) [80]byte {
	cb := make([]byte, 0, len(j.coinb1)+len(en1)+len(en2)+len(j.coinb2))
	cb = append(append(append(append(cb, j.coinb1...), en1...), en2...), j.coinb2...)
	root := sha256d(cb)
	for _, b := range j.branch {
		root = sha256d(append(root[:], b...))
	}
	var h [80]byte
	binary.LittleEndian.PutUint32(h[0:], version)
	copy(h[4:36], j.prevHash)
	copy(h[36:68], root[:])
	binary.LittleEndian.PutUint32(h[68:], j.ntime)
	binary.LittleEndian.PutUint32(h[72:], j.bits)
	binary.LittleEndian.PutUint32(h[76:], nonce)
	return h
}

// powValue is the header's proof-of-work hash as a number.
func powValue(h [80]byte, scrypt bool) *big.Int {
	var le [32]byte
	if scrypt {
		le = pow.Scrypt.PoWHash(h[:])
	} else {
		le = sha256d(h[:])
	}
	for i, j := 0, 31; i < j; i, j = i+1, j-1 {
		le[i], le[j] = le[j], le[i]
	}
	return new(big.Int).SetBytes(le[:])
}

var (
	diff1SHA    = new(big.Int).Lsh(big.NewInt(0xffff), 208)
	diff1Scrypt = new(big.Int).Lsh(big.NewInt(0xffff), 224)
)

func shareTarget(d float64, scrypt bool) *big.Int {
	d1 := diff1SHA
	if scrypt {
		d1 = diff1Scrypt
	}
	f := new(big.Float).SetPrec(512).SetInt(d1)
	f.Quo(f, new(big.Float).SetPrec(512).SetFloat64(d))
	t, _ := f.Int(nil)
	return t
}

func compactTarget(bits uint32) *big.Int {
	exp := uint(bits >> 24)
	mant := big.NewInt(int64(bits & 0x007fffff))
	if exp <= 3 {
		return mant.Rsh(mant, 8*(3-exp))
	}
	return mant.Lsh(mant, 8*(exp-3))
}

// share finds a nonce meeting the share target but missing the network
// target (so no block is found). It gives up after a few thousand tries
// (the test difficulty is expected to be tiny).
// With a version-rolling mask (NiceHash on SHA-256d) the header carries
// random rolled bits and the submit reports them (BIP310: version & mask).
func (c *conn) share(en2ctr uint64, scrypt bool) (*job, []byte, uint32, string, bool) {
	c.mu.Lock()
	j, en1, size, d, mask := c.job, c.en1, c.en2Size, c.diff, c.mask
	c.mu.Unlock()
	if j == nil || d <= 0 {
		return nil, nil, 0, "", false
	}
	version, vhex := j.version, ""
	if mask != 0 {
		version = j.version&^mask | rand.Uint32()&mask
		vhex = fmt.Sprintf("%08x", version&mask)
	}
	en2 := make([]byte, size)
	binary.BigEndian.PutUint64(en2[size-8:], en2ctr)
	st, nt := shareTarget(d, scrypt), compactTarget(j.bits)
	start := rand.Uint32()
	for i := uint32(0); i < 4096; i++ {
		v := powValue(header(j, en1, en2, version, start+i), scrypt)
		if v.Cmp(st) <= 0 && v.Cmp(nt) > 0 {
			return j, en2, start + i, vhex, true
		}
	}
	return nil, nil, 0, "", false
}

type stats struct {
	mu        sync.Mutex
	hs        []time.Duration
	sub       []time.Duration
	rejected  map[string]uint64
	fails     map[string]int
	diffs     map[string]int
	rolled    uint64
	submitted atomic.Uint64
	accepted  atomic.Uint64
}

func (s *stats) fail(err error) {
	s.mu.Lock()
	s.fails[err.Error()]++
	s.mu.Unlock()
}

func pct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[int(math.Min(float64(len(d)-1), math.Floor(p*float64(len(d)))))]
}

// Run opens cfg.Conns connections, runs the handshake on each, then submits
// shares at cfg.ShareRate per connection for cfg.Duration.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.Conns < 1 {
		return nil, errors.New("conns must be >= 1")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.NameFmt == "" {
		cfg.NameFmt = "rentx.rig%05d"
	}
	if cfg.Profile == "" {
		cfg.Profile = Mixed
	}
	st := &stats{rejected: map[string]uint64{}, fails: map[string]int{}, diffs: map[string]int{}}
	conns := make([]*conn, cfg.Conns)
	var wg sync.WaitGroup
	var refused atomic.Int64
	gap := time.Duration(0)
	if cfg.Ramp > 0 {
		gap = cfg.Ramp / time.Duration(cfg.Conns)
	}
	sem := make(chan struct{}, 256) // concurrent handshakes
	for i := 0; i < cfg.Conns; i++ {
		if gap > 0 {
			time.Sleep(gap)
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			src := ""
			if len(cfg.SourceIPs) > 0 {
				src = cfg.SourceIPs[i%len(cfg.SourceIPs)]
			}
			p := cfg.Profile
			if p == Mixed {
				p = []Profile{NiceHash, MRR}[i%2]
			}
			t0 := time.Now()
			c, err := dial(ctx, src, cfg.Addr)
			if err != nil {
				st.fail(err)
				return
			}
			if err := c.handshake(ctx, &cfg, p, workerName(&cfg, i)); err != nil {
				// The server closes over-limit connections at once, before
				// answering anything.
				over := false
				select {
				case <-c.closed:
					over = !c.answered.Load()
				case <-time.After(time.Second):
				}
				c.c.Close()
				if over {
					refused.Add(1)
				} else {
					st.fail(err)
				}
				return
			}
			d := time.Since(t0)
			st.mu.Lock()
			st.hs = append(st.hs, d)
			st.mu.Unlock()
			conns[i] = c
		}(i)
	}
	wg.Wait()
	res := &Result{Refused: int(refused.Load())}
	var live []*conn
	for _, c := range conns {
		if c != nil {
			live = append(live, c)
		}
	}
	res.Opened = len(live)
	defer func() {
		for _, c := range live {
			c.c.Close()
		}
	}()

	t0 := time.Now()
	sctx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()
	if cfg.ShareRate > 0 {
		for i, c := range live {
			wg.Add(1)
			go func(i int, c *conn) {
				defer wg.Done()
				worker := workerName(&cfg, i)
				ctr := uint64(i) << 32
				// Poisson arrivals at ShareRate on an absolute schedule, each
				// submit in its own goroutine (pipelined, as a big miner on one
				// connection sends them), at most 512 in flight.
				inflight := make(chan struct{}, 512)
				var sw sync.WaitGroup
				defer sw.Wait()
				due := time.Now().Add(time.Duration(rand.Float64() * float64(time.Second) / cfg.ShareRate))
				for {
					if wait := time.Until(due); wait > 0 {
						select {
						case <-sctx.Done():
							return
						case <-c.closed:
							return
						case <-time.After(wait):
						}
					} else if sctx.Err() != nil {
						return
					} else {
						select {
						case <-c.closed:
							return
						default:
						}
					}
					due = due.Add(time.Duration(rand.ExpFloat64() * float64(time.Second) / cfg.ShareRate))
					ctr++
					j, en2, nonce, vhex, ok := c.share(ctr, cfg.Scrypt)
					if !ok {
						continue
					}
					params := []any{worker, j.id, hex.EncodeToString(en2), fmt.Sprintf("%08x", j.ntime), fmt.Sprintf("%08x", nonce)}
					if vhex != "" {
						params = append(params, vhex)
					}
					inflight <- struct{}{}
					sw.Add(1)
					go func() {
						defer sw.Done()
						defer func() { <-inflight }()
						ts := time.Now()
						r, err := c.call(ctx, cfg.Timeout, "mining.submit", params...)
						d := time.Since(ts)
						st.submitted.Add(1)
						if err != nil {
							if sctx.Err() == nil {
								st.fail(err)
							}
							return
						}
						st.mu.Lock()
						st.sub = append(st.sub, d)
						if r.ok() {
							st.accepted.Add(1)
							if vhex != "" {
								st.rolled++
							}
						} else {
							st.rejected[r.errText()]++
						}
						st.mu.Unlock()
					}()
				}
			}(i, c)
		}
		wg.Wait()
	} else {
		<-sctx.Done()
	}
	res.Elapsed = time.Since(t0)
	for _, c := range live {
		select {
		case <-c.closed:
			res.Disconnected++
		default:
		}
		c.mu.Lock()
		d := c.diff
		c.mu.Unlock()
		st.mu.Lock()
		st.diffs[strconv.FormatFloat(d, 'g', -1, 64)]++
		st.mu.Unlock()
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	res.HandshakeP50, res.HandshakeP99 = pct(st.hs, 0.5), pct(st.hs, 0.99)
	res.SubmitP50, res.SubmitP99, res.SubmitMax = pct(st.sub, 0.5), pct(st.sub, 0.99), pct(st.sub, 1)
	res.Submitted, res.Accepted = st.submitted.Load(), st.accepted.Load()
	res.Rejected, res.FailReasons, res.Difficulties = st.rejected, st.fails, st.diffs
	res.VersionRolled = st.rolled
	for _, c := range live {
		c.mu.Lock()
		if res.MinExtranonce2Size == 0 || c.en2Size < res.MinExtranonce2Size {
			res.MinExtranonce2Size = c.en2Size
		}
		c.mu.Unlock()
	}
	for _, n := range st.fails {
		res.Failed += n
	}
	return res, nil
}

// workerName is connection i's worker: rental rig names, or one shared name
// (MRR rigs rented under one pool profile).
func workerName(cfg *Config, i int) string {
	if cfg.SameName {
		return fmt.Sprintf(cfg.NameFmt, 0)
	}
	return fmt.Sprintf(cfg.NameFmt, i)
}
