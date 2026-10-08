// Package stratum implements a Stratum V1 mining server for solo mining,
// including BIP310 version rolling (mining.configure).
package stratum

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/slippybogle/wizard-blocks/internal/bitcoin"
	"github.com/slippybogle/wizard-blocks/internal/stats"
	"github.com/slippybogle/wizard-blocks/internal/work"
)

// Payout is the destination a connection's blocks pay to.
type Payout struct {
	Script  []byte
	Address string
}

// PayoutResolver maps a Stratum username to a payout. It must validate the
// address (the engine cross-checks with the node) and return an error for
// anything that cannot safely receive a block reward.
type PayoutResolver func(ctx context.Context, username string) (*Payout, error)

// Config configures the server.
type Config struct {
	Listen             string
	Extranonce2Size    int
	VersionRollingMask uint32
	MaxConnections     int
	MaxConnsPerIP      int
	AuthTimeout        time.Duration
	IdleTimeout        time.Duration
	MaxLineBytes       int
	MsgRate            float64 // messages per second (token bucket refill)
	MsgBurst           float64
	Vardiff            VardiffConfig
}

// Server accepts miner connections.
type Server struct {
	cfg     Config
	mgr     *work.Manager
	resolve PayoutResolver
	st      *stats.Collector
	log     *slog.Logger

	ln net.Listener

	mu       sync.Mutex
	sessions map[*Session]struct{}
	perIP    map[string]int
	en1InUse map[uint32]struct{}
	en1Next  uint32
	closing  bool

	dupMu sync.Mutex
	dups  map[uint64]map[bitcoin.Hash]struct{} // generation -> header hashes

	wg sync.WaitGroup
}

// NewServer creates a server; call Listen then Serve.
func NewServer(cfg Config, mgr *work.Manager, resolve PayoutResolver, st *stats.Collector, log *slog.Logger) *Server {
	var seed [4]byte
	_, _ = rand.Read(seed[:])
	s := &Server{
		cfg: cfg, mgr: mgr, resolve: resolve, st: st, log: log,
		sessions: map[*Session]struct{}{}, perIP: map[string]int{}, en1InUse: map[uint32]struct{}{},
		en1Next: binary.BigEndian.Uint32(seed[:]),
		dups:    map[uint64]map[bitcoin.Hash]struct{}{},
	}
	mgr.OnWork(s.broadcast)
	return s
}

// Listen binds the listening socket.
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	s.ln = ln
	return nil
}

// Addr returns the bound address.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Serve accepts connections until Close.
func (s *Server) Serve(ctx context.Context) error {
	go s.housekeeping(ctx)
	var backoff time.Duration
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closing := s.closing
			s.mu.Unlock()
			if closing || errors.Is(err, net.ErrClosed) {
				return nil
			}
			// Temporary errors (e.g. EMFILE): back off instead of spinning.
			if backoff == 0 {
				backoff = 5 * time.Millisecond
			} else if backoff < time.Second {
				backoff *= 2
			}
			s.log.Warn("accept error", "err", err)
			time.Sleep(backoff)
			continue
		}
		backoff = 0
		s.admit(ctx, conn)
	}
}

func (s *Server) admit(ctx context.Context, conn net.Conn) {
	ip, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	s.mu.Lock()
	if s.closing || len(s.sessions) >= s.cfg.MaxConnections || s.perIP[ip] >= s.cfg.MaxConnsPerIP {
		n := len(s.sessions)
		s.mu.Unlock()
		s.log.Warn("connection refused (limit)", "ip", ip, "open", n)
		conn.Close()
		return
	}
	en1 := s.allocEn1Locked()
	sess := newSession(s, conn, ip, en1)
	s.sessions[sess] = struct{}{}
	s.perIP[ip]++
	n := len(s.sessions)
	s.wg.Add(1)
	s.mu.Unlock()
	s.st.SetConnections(n)
	go func() {
		defer s.wg.Done()
		sess.run(ctx)
		s.remove(sess)
	}()
}

func (s *Server) allocEn1Locked() uint32 {
	for {
		s.en1Next++
		if _, used := s.en1InUse[s.en1Next]; !used {
			s.en1InUse[s.en1Next] = struct{}{}
			return s.en1Next
		}
	}
}

func (s *Server) remove(sess *Session) {
	s.mu.Lock()
	delete(s.sessions, sess)
	delete(s.en1InUse, sess.en1)
	if s.perIP[sess.ip]--; s.perIP[sess.ip] <= 0 {
		delete(s.perIP, sess.ip)
	}
	n := len(s.sessions)
	s.mu.Unlock()
	s.st.SetConnections(n)
}

// Close stops accepting and disconnects all miners.
func (s *Server) Close() {
	s.mu.Lock()
	s.closing = true
	all := make([]*Session, 0, len(s.sessions))
	for sess := range s.sessions {
		all = append(all, sess)
	}
	s.mu.Unlock()
	if s.ln != nil {
		s.ln.Close()
	}
	for _, sess := range all {
		sess.close("server shutdown")
	}
	s.wg.Wait()
}

func (s *Server) snapshotSessions() []*Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Session, 0, len(s.sessions))
	for sess := range s.sessions {
		out = append(out, sess)
	}
	return out
}

// broadcast pushes new work to every ready session.
func (s *Server) broadcast(w *work.Work) {
	if w.Clean {
		s.dupMu.Lock()
		for g := range s.dups {
			if g+1 < w.Gen {
				delete(s.dups, g)
			}
		}
		s.dupMu.Unlock()
	}
	for _, sess := range s.snapshotSessions() {
		sess.sendWork(w)
	}
}

// seen records a share's header hash and reports whether it was already seen.
func (s *Server) seen(gen uint64, h bitcoin.Hash) bool {
	s.dupMu.Lock()
	defer s.dupMu.Unlock()
	m := s.dups[gen]
	if m == nil {
		m = map[bitcoin.Hash]struct{}{}
		s.dups[gen] = m
	}
	if _, ok := m[h]; ok {
		return true
	}
	if len(m) >= 4_000_000 { // memory bound; at this size a duplicate is astronomically unlikely to matter
		clear(m)
	}
	m[h] = struct{}{}
	return false
}

func (s *Server) housekeeping(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	lastPrune := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now()
			for _, sess := range s.snapshotSessions() {
				sess.tick(now)
			}
			if now.Sub(lastPrune) > time.Hour {
				s.st.Prune(24 * time.Hour)
				lastPrune = now
			}
		}
	}
}
