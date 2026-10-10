// Package testminer is a CPU Stratum V1 client used by the regtest
// integration harness (and cmd/testminer). Its share/header construction is
// deliberately implemented from scratch with crypto/sha256 only — following
// the cgminer algorithm — so it acts as an independent check of the server.
package testminer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/pow"
)

// Job is a parsed mining.notify.
type Job struct {
	ID       string
	PrevHash []byte // header byte order (words swapped back)
	Coinb1   []byte
	Coinb2   []byte
	Branch   [][]byte
	Version  uint32
	Bits     uint32
	NTime    uint32
	Clean    bool
	Received time.Time
}

// Response is a JSON-RPC response.
type Response struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// ErrCode extracts the Stratum error code (0 if none).
func (r *Response) ErrCode() int {
	var e []any
	if json.Unmarshal(r.Error, &e) != nil || len(e) == 0 {
		return 0
	}
	f, _ := e[0].(float64)
	return int(f)
}

// OK reports whether the result is true and there is no error.
func (r *Response) OK() bool { return string(r.Result) == "true" && r.ErrCode() == 0 }

// Client is a Stratum V1 client.
type Client struct {
	conn net.Conn
	wmu  sync.Mutex
	id   atomic.Uint64

	pmu     sync.Mutex
	pending map[uint64]chan *Response

	mu      sync.Mutex
	en1     []byte
	en2Size int
	mask    uint32
	diff    float64
	job     *Job
	jobs    chan *Job

	closed chan struct{}
	err    error
}

// Dial connects to a Stratum server.
func Dial(addr string) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, pending: map[uint64]chan *Response{}, jobs: make(chan *Job, 64), closed: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

// Close closes the connection.
func (c *Client) Close() { c.conn.Close() }

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} { return c.closed }

func (c *Client) readLoop() {
	defer close(c.closed)
	r := bufio.NewReaderSize(c.conn, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			c.err = err
			return
		}
		var m struct {
			ID     *uint64           `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
			Result json.RawMessage   `json:"result"`
			Error  json.RawMessage   `json:"error"`
		}
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		if m.Method != "" {
			c.handleNotification(m.Method, m.Params)
			continue
		}
		if m.ID == nil {
			continue
		}
		c.pmu.Lock()
		ch := c.pending[*m.ID]
		delete(c.pending, *m.ID)
		c.pmu.Unlock()
		if ch != nil {
			ch <- &Response{ID: *m.ID, Result: m.Result, Error: m.Error}
		}
	}
}

func (c *Client) handleNotification(method string, params []json.RawMessage) {
	switch method {
	case "mining.set_difficulty":
		var d float64
		if len(params) > 0 && json.Unmarshal(params[0], &d) == nil {
			c.mu.Lock()
			c.diff = d
			c.mu.Unlock()
		}
	case "mining.notify":
		j, err := parseNotify(params)
		if err != nil {
			return
		}
		c.mu.Lock()
		c.job = j
		c.mu.Unlock()
		select {
		case c.jobs <- j:
		default:
		}
	}
}

func parseNotify(p []json.RawMessage) (*Job, error) {
	if len(p) < 9 {
		return nil, errors.New("short notify")
	}
	var id, prev, cb1, cb2, ver, bits, ntime string
	var branch []string
	var clean bool
	for i, dst := range []any{&id, &prev, &cb1, &cb2, &branch, &ver, &bits, &ntime, &clean} {
		if err := json.Unmarshal(p[i], dst); err != nil {
			return nil, err
		}
	}
	j := &Job{ID: id, Clean: clean, Received: time.Now()}
	var err error
	ph, err := hex.DecodeString(prev)
	if err != nil || len(ph) != 32 {
		return nil, errors.New("bad prevhash")
	}
	// cgminer: swap32 each word of the notify prevhash to get header bytes.
	j.PrevHash = make([]byte, 32)
	for i := 0; i < 8; i++ {
		binary.LittleEndian.PutUint32(j.PrevHash[i*4:], binary.BigEndian.Uint32(ph[i*4:]))
	}
	if j.Coinb1, err = hex.DecodeString(cb1); err != nil {
		return nil, err
	}
	if j.Coinb2, err = hex.DecodeString(cb2); err != nil {
		return nil, err
	}
	for _, b := range branch {
		h, err := hex.DecodeString(b)
		if err != nil || len(h) != 32 {
			return nil, errors.New("bad branch")
		}
		j.Branch = append(j.Branch, h)
	}
	v, err1 := strconv.ParseUint(ver, 16, 32)
	nb, err2 := strconv.ParseUint(bits, 16, 32)
	nt, err3 := strconv.ParseUint(ntime, 16, 32)
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, errors.New("bad version/bits/time")
	}
	j.Version, j.Bits, j.NTime = uint32(v), uint32(nb), uint32(nt)
	return j, nil
}

// Call sends a request and waits for its response.
func (c *Client) Call(ctx context.Context, method string, params ...any) (*Response, error) {
	if params == nil {
		params = []any{}
	}
	id := c.id.Add(1)
	ch := make(chan *Response, 1)
	c.pmu.Lock()
	c.pending[id] = ch
	c.pmu.Unlock()
	b, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := c.SendRaw(append(b, '\n')); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r, nil
	case <-c.closed:
		return nil, fmt.Errorf("connection closed: %v", c.err)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// CallRawParams sends a request whose params are given as raw JSON.
func (c *Client) CallRawParams(ctx context.Context, method string, params json.RawMessage) (*Response, error) {
	id := c.id.Add(1)
	ch := make(chan *Response, 1)
	c.pmu.Lock()
	c.pending[id] = ch
	c.pmu.Unlock()
	line := fmt.Sprintf(`{"id":%d,"method":%q,"params":%s}`+"\n", id, method, params)
	if err := c.SendRaw([]byte(line)); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r, nil
	case <-c.closed:
		return nil, fmt.Errorf("connection closed: %v", c.err)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// CallMany writes several requests in a single write (pipelined) and waits
// for all responses, returned in request order.
func (c *Client) CallMany(ctx context.Context, method string, paramsList ...[]any) ([]*Response, error) {
	var buf []byte
	chans := make([]chan *Response, len(paramsList))
	for i, p := range paramsList {
		id := c.id.Add(1)
		chans[i] = make(chan *Response, 1)
		c.pmu.Lock()
		c.pending[id] = chans[i]
		c.pmu.Unlock()
		b, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": p})
		buf = append(append(buf, b...), '\n')
	}
	if err := c.SendRaw(buf); err != nil {
		return nil, err
	}
	out := make([]*Response, len(chans))
	for i, ch := range chans {
		select {
		case out[i] = <-ch:
		case <-c.closed:
			return nil, fmt.Errorf("connection closed: %v", c.err)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return out, nil
}

// SendRaw writes raw bytes (used to inject malformed input in tests).
func (c *Client) SendRaw(b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.conn.Write(b)
	return err
}

// Configure negotiates BIP310 version rolling and returns the granted mask.
func (c *Client) Configure(ctx context.Context, mask uint32) (uint32, error) {
	r, err := c.Call(ctx, "mining.configure", []string{"version-rolling"},
		map[string]any{"version-rolling.mask": fmt.Sprintf("%08x", mask), "version-rolling.min-bit-count": 2})
	if err != nil {
		return 0, err
	}
	var res map[string]any
	if err := json.Unmarshal(r.Result, &res); err != nil {
		return 0, fmt.Errorf("configure result: %s", r.Result)
	}
	if ok, _ := res["version-rolling"].(bool); !ok {
		return 0, errors.New("version rolling refused")
	}
	ms, _ := res["version-rolling.mask"].(string)
	m, err := strconv.ParseUint(ms, 16, 32)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	c.mask = uint32(m)
	c.mu.Unlock()
	return uint32(m), nil
}

// Subscribe performs mining.subscribe.
func (c *Client) Subscribe(ctx context.Context) error {
	r, err := c.Call(ctx, "mining.subscribe", "wizard-testminer/1.0")
	if err != nil {
		return err
	}
	var res []json.RawMessage
	if err := json.Unmarshal(r.Result, &res); err != nil || len(res) < 3 {
		return fmt.Errorf("subscribe result: %s %s", r.Result, r.Error)
	}
	var en1 string
	var size int
	if json.Unmarshal(res[1], &en1) != nil || json.Unmarshal(res[2], &size) != nil {
		return errors.New("subscribe result fields")
	}
	b, err := hex.DecodeString(en1)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.en1, c.en2Size = b, size
	c.mu.Unlock()
	return nil
}

// Authorize performs mining.authorize.
func (c *Client) Authorize(ctx context.Context, user, pass string) (*Response, error) {
	return c.Call(ctx, "mining.authorize", user, pass)
}

// Submit sends mining.submit with explicit fields (versionHex may be "").
func (c *Client) Submit(ctx context.Context, worker, jobID, en2Hex, ntimeHex, nonceHex, versionHex string) (*Response, error) {
	if versionHex == "" {
		return c.Call(ctx, "mining.submit", worker, jobID, en2Hex, ntimeHex, nonceHex)
	}
	return c.Call(ctx, "mining.submit", worker, jobID, en2Hex, ntimeHex, nonceHex, versionHex)
}

// State returns the current extranonce1, extranonce2 size, mask, difficulty and job.
func (c *Client) State() (en1 []byte, en2Size int, mask uint32, diff float64, job *Job) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.en1, c.en2Size, c.mask, c.diff, c.job
}

// WaitJob waits for the next mining.notify.
func (c *Client) WaitJob(ctx context.Context) (*Job, error) {
	select {
	case j := <-c.jobs:
		return j, nil
	case <-c.closed:
		return nil, fmt.Errorf("connection closed: %v", c.err)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// WaitJobOtherThan waits until the latest job's ID differs from prevID.
// mining.set_difficulty and the mining.notify that follows it are separate
// lines, so the new difficulty can be visible in State before the job is.
func (c *Client) WaitJobOtherThan(ctx context.Context, prevID string) (*Job, error) {
	for {
		if _, _, _, _, j := c.State(); j != nil && j.ID != prevID {
			return j, nil
		}
		select {
		case <-c.jobs:
		case <-c.closed:
			return nil, fmt.Errorf("connection closed: %v", c.err)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// DrainJobs discards queued notifications and returns the latest job.
func (c *Client) DrainJobs() *Job {
	for {
		select {
		case <-c.jobs:
		default:
			_, _, _, _, j := c.State()
			return j
		}
	}
}

func sha256d(b []byte) [32]byte {
	h := sha256.Sum256(b)
	return sha256.Sum256(h[:])
}

// Header builds the 80-byte header for a job (cgminer algorithm).
func Header(j *Job, en1, en2 []byte, version, ntime, nonce uint32) [80]byte {
	cb := make([]byte, 0, len(j.Coinb1)+len(en1)+len(en2)+len(j.Coinb2))
	cb = append(append(append(append(cb, j.Coinb1...), en1...), en2...), j.Coinb2...)
	root := sha256d(cb)
	for _, b := range j.Branch {
		root = sha256d(append(root[:], b...))
	}
	var h [80]byte
	binary.LittleEndian.PutUint32(h[0:], version)
	copy(h[4:36], j.PrevHash)
	copy(h[36:68], root[:])
	binary.LittleEndian.PutUint32(h[68:], ntime)
	binary.LittleEndian.PutUint32(h[72:], j.Bits)
	binary.LittleEndian.PutUint32(h[76:], nonce)
	return h
}

// scryptBE returns the Scrypt PoW hash of a header as a big-endian number's
// bytes, like HashBE.
func scryptBE(h [80]byte) [32]byte {
	pw := pow.Scrypt.PoWHash(h[:])
	var out [32]byte
	for i := 0; i < 32; i++ {
		out[i] = pw[31-i]
	}
	return out
}

// HashBE returns the header hash as a big-endian number's bytes (display order).
func HashBE(h [80]byte) [32]byte {
	d := sha256d(h[:])
	var be [32]byte
	for i := 0; i < 32; i++ {
		be[i] = d[31-i]
	}
	return be
}

func leadingZeroBits(b [32]byte) int {
	n := 0
	for _, x := range b {
		if x == 0 {
			n += 8
			continue
		}
		for x&0x80 == 0 {
			n++
			x <<= 1
		}
		break
	}
	return n
}

var diff1 = new(big.Int).Lsh(big.NewInt(0xffff), 208)

// ShareTarget converts a Stratum difficulty to a target.
func ShareTarget(d float64) *big.Int {
	f := new(big.Float).SetPrec(512).SetInt(diff1)
	f.Quo(f, new(big.Float).SetPrec(512).SetFloat64(d))
	t, _ := f.Int(nil)
	return t
}

// CompactTarget decodes nBits (no sign/overflow handling needed for tests).
func CompactTarget(bits uint32) *big.Int {
	exp := uint(bits >> 24)
	mant := big.NewInt(int64(bits & 0x007fffff))
	if exp <= 3 {
		return mant.Rsh(mant, 8*(3-exp))
	}
	return mant.Lsh(mant, 8*(exp-3))
}

// LeadingZeroBits counts leading zero bits of a big-endian hash.
func LeadingZeroBits(b [32]byte) int { return leadingZeroBits(b) }

// Grind searches nonces for a header whose big-endian hash satisfies pred.
func Grind(j *Job, en1, en2 []byte, version, ntime uint32, pred func(be [32]byte) bool) (uint32, [32]byte, bool) {
	return GrindFrom(j, en1, en2, version, ntime, 0, pred)
}

// GrindFrom is Grind starting at nonce start.
func GrindFrom(j *Job, en1, en2 []byte, version, ntime, start uint32, pred func(be [32]byte) bool) (uint32, [32]byte, bool) {
	for nonce := start; nonce < start+1<<26; nonce++ {
		be := HashBE(Header(j, en1, en2, version, ntime, nonce))
		if pred(be) {
			return nonce, be, true
		}
	}
	return 0, [32]byte{}, false
}

// MineOptions configures Mine.
type MineOptions struct {
	Worker      string
	Threads     int
	MinZeroBits int // only submit shares whose hash has at least this many leading zero bits
	// Scrypt mines Litecoin-style: targets are checked against the Scrypt
	// hash and share difficulty 1 is 2^16 times easier. Callbacks still get
	// the block's identity hash (SHA-256d).
	Scrypt      bool
	RollVersion bool // roll version bits inside the negotiated mask
	// VersionMode selects how rolled versions are built and reported:
	//   "bip310" (default): version = (job & ~mask) | x; submit version & mask
	//   "xor"  (ESP-Miner): version = (job & ~mask) | x; submit version ^ job
	//   "or"   (cgminer):   version = job | x;           submit x
	VersionMode string
	// OneBlockPerPrevHash: after submitting a block-solving share, submit no
	// further block-solving shares on the same previous block (avoids racing
	// our own blocks at the same height on regtest, where most shares solve).
	OneBlockPerPrevHash bool
	// NonBlockShares submits only shares that do NOT solve a block (hash
	// above the network target), one per ShareInterval. On regtest every
	// normal share is also a block; this exercises share-only paths
	// (luck, creature tiers). Requires a share difficulty below network.
	NonBlockShares bool
	ShareInterval  time.Duration
	// OnResult is called for every submit response.
	OnResult func(job *Job, r *Response, hashHex string, version uint32)
	// OnSubmit is called just before each share is sent.
	OnSubmit func(job *Job, hashHex string, version uint32)
}

// Mine hashes the current job until ctx ends or the connection closes.
func (c *Client) Mine(ctx context.Context, o MineOptions) error {
	if o.Threads < 1 {
		o.Threads = 1
	}
	var wg sync.WaitGroup
	var gen atomic.Uint64
	var solved sync.Map
	errc := make(chan error, o.Threads)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.closed:
				return
			case j := <-c.jobs:
				c.mu.Lock()
				c.job = j
				c.mu.Unlock()
				gen.Add(1)
			}
		}
	}()
	for t := 0; t < o.Threads; t++ {
		wg.Add(1)
		go func(thread int) {
			defer wg.Done()
			errc <- c.mineThread(ctx, o, thread, &gen, &solved)
		}(t)
	}
	wg.Wait()
	select {
	case err := <-errc:
		return err
	default:
		return nil
	}
}

func (c *Client) mineThread(ctx context.Context, o MineOptions, thread int, gen *atomic.Uint64, solved *sync.Map) error {
	var en2Counter uint64
	for {
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-c.closed:
			return fmt.Errorf("connection closed: %v", c.err)
		default:
		}
		en1, en2Size, mask, diff, job := c.State()
		if job == nil || diff == 0 {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		g := gen.Load()
		en2 := make([]byte, en2Size)
		en2Counter++
		v := uint64(thread)<<56 | en2Counter
		for i := 0; i < en2Size && i < 8; i++ {
			en2[en2Size-1-i] = byte(v >> (8 * i))
		}
		if en2Size >= 1 {
			en2[0] = byte(thread)
		}
		target := ShareTarget(diff)
		if o.Scrypt {
			target = pow.Scrypt.ShareTarget(diff)
		}
		netTarget := CompactTarget(job.Bits)
		version := job.Version
		var rollBits uint32
		if o.RollVersion && mask != 0 {
			// Spread rolled values over the mask bits: (job & ~mask) | (x & mask).
			x := uint32(en2Counter*0x9e3779b1) << 13
			rollBits = x & mask
			if o.VersionMode == "or" {
				version = job.Version | x&mask
			} else {
				version = job.Version&^mask | x&mask
			}
		}
		ntime := job.NTime
		for nonce := uint32(0); ; nonce++ {
			if nonce&0x3fff == 0 && (gen.Load() != g || ctx.Err() != nil) {
				break
			}
			h := Header(job, en1, en2, version, ntime, nonce)
			be := HashBE(h) // checked against targets
			id := be        // the block hash reported to callbacks
			if o.Scrypt {
				be = scryptBE(h)
			}
			if o.NonBlockShares {
				hv := new(big.Int).SetBytes(be[:])
				if hv.Cmp(netTarget) <= 0 || hv.Cmp(target) > 0 {
					if nonce == 0xffffffff {
						break
					}
					continue
				}
				if o.OnSubmit != nil {
					o.OnSubmit(job, hex.EncodeToString(id[:]), version)
				}
				r, err := c.Submit(ctx, o.Worker, job.ID, hex.EncodeToString(en2), fmt.Sprintf("%08x", ntime), fmt.Sprintf("%08x", nonce), vhexFor(version, job.Version, mask, rollBits, o.VersionMode))
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
				if o.OnResult != nil {
					o.OnResult(job, r, hex.EncodeToString(id[:]), version)
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(o.ShareInterval):
				}
				break
			}
			if leadingZeroBits(be) >= o.MinZeroBits && new(big.Int).SetBytes(be[:]).Cmp(target) <= 0 {
				if o.OneBlockPerPrevHash && new(big.Int).SetBytes(be[:]).Cmp(netTarget) <= 0 {
					if _, done := solved.LoadOrStore(string(job.PrevHash), true); done {
						// Already solved this height; wait for the next job.
						for gen.Load() == g && ctx.Err() == nil {
							select {
							case <-c.closed:
								return fmt.Errorf("connection closed: %v", c.err)
							case <-time.After(5 * time.Millisecond):
							}
						}
						break
					}
				}
				vhex := ""
				if mask != 0 {
					bits := version & mask
					switch o.VersionMode {
					case "xor":
						bits = version ^ job.Version
					case "or":
						bits = rollBits // cgminer reports the mask bits it OR'd in
					}
					vhex = fmt.Sprintf("%08x", bits)
				}
				if o.OnSubmit != nil {
					o.OnSubmit(job, hex.EncodeToString(id[:]), version)
				}
				r, err := c.Submit(ctx, o.Worker, job.ID, hex.EncodeToString(en2),
					fmt.Sprintf("%08x", ntime), fmt.Sprintf("%08x", nonce), vhex)
				if err != nil {
					return err
				}
				if o.OnResult != nil {
					o.OnResult(job, r, hex.EncodeToString(id[:]), version)
				}
				break // fresh extranonce2 for the next share
			}
			if nonce == 0xffffffff {
				break
			}
		}
	}
}

// vhexFor encodes submitted version bits for the given mode ("" if no mask).
func vhexFor(version, jobVersion, mask, rollBits uint32, mode string) string {
	if mask == 0 {
		return ""
	}
	bits := version & mask
	switch mode {
	case "xor":
		bits = version ^ jobVersion
	case "or":
		bits = rollBits
	}
	return fmt.Sprintf("%08x", bits)
}
