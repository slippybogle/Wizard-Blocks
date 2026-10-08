package node

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"time"
)

// This is a minimal ZMTP 3.0 (https://rfc.zeromq.org/spec/23/) SUB client
// using the NULL security mechanism — exactly what bitcoind's
// -zmqpub* endpoints speak. Implementing it directly avoids cgo/libzmq and
// keeps the binary static. Polling remains active as a fallback, so a broken
// ZMQ connection can only delay (never prevent) new-block detection.

const (
	zmtpFlagMore    = 0x01
	zmtpFlagLong    = 0x02
	zmtpFlagCommand = 0x04
	zmtpMaxFrame    = 4 << 20 // hashblock frames are tiny; refuse anything absurd
)

// ZMQMessage is one multipart message published by the node.
type ZMQMessage struct {
	Topic string
	Body  []byte
	Seq   uint32
}

// ZMQSubscriber maintains a SUB connection with automatic reconnect.
type ZMQSubscriber struct {
	Endpoint string
	Topics   []string
	Log      *slog.Logger

	connected atomic.Bool
	received  atomic.Uint64
}

// Connected reports whether a ZMTP session is currently established.
func (z *ZMQSubscriber) Connected() bool { return z.connected.Load() }

// Received returns the number of messages received.
func (z *ZMQSubscriber) Received() uint64 { return z.received.Load() }

// Run connects and delivers messages to out until ctx is done.
func (z *ZMQSubscriber) Run(ctx context.Context, out chan<- ZMQMessage) {
	backoff := 500 * time.Millisecond
	for ctx.Err() == nil {
		err := z.session(ctx, out)
		z.connected.Store(false)
		if ctx.Err() != nil {
			return
		}
		z.Log.Warn("zmq disconnected; polling fallback active", "endpoint", z.Endpoint, "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 10*time.Second {
			backoff *= 2
		}
	}
}

func (z *ZMQSubscriber) session(ctx context.Context, out chan<- ZMQMessage) error {
	addr, ok := strings.CutPrefix(z.Endpoint, "tcp://")
	if !ok {
		return fmt.Errorf("unsupported zmq endpoint %q (only tcp://)", z.Endpoint)
	}
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	// Handshake must complete promptly; afterwards the publisher may be
	// silent for a long time (no blocks), so no read deadline is kept.
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	if err := zmtpHandshake(conn, r, "SUB"); err != nil {
		return fmt.Errorf("handshake: %w", err)
	}
	for _, t := range z.Topics {
		// ZMTP 3.0 subscription: a message whose first byte is 1, followed by the topic.
		if err := writeFrame(conn, 0, append([]byte{1}, t...)); err != nil {
			return err
		}
	}
	_ = conn.SetDeadline(time.Time{})
	z.connected.Store(true)
	z.Log.Info("zmq connected", "endpoint", z.Endpoint, "topics", z.Topics)

	for {
		parts, err := readMessage(r)
		if err != nil {
			return err
		}
		if parts == nil {
			continue // command frame
		}
		if len(parts) < 2 {
			continue
		}
		m := ZMQMessage{Topic: string(parts[0]), Body: parts[1]}
		if len(parts) >= 3 && len(parts[2]) == 4 {
			m.Seq = binary.LittleEndian.Uint32(parts[2])
		}
		z.received.Add(1)
		select {
		case out <- m:
		default: // consumer busy; it re-reads the tip anyway
		}
	}
}

func zmtpGreeting() []byte {
	g := make([]byte, 64)
	g[0] = 0xff
	g[9] = 0x7f
	g[10] = 3 // version major
	g[11] = 0 // version minor: 3.0 so libzmq uses message-style subscriptions
	copy(g[12:32], "NULL")
	// g[32] as-server = 0; rest filler zeros
	return g
}

func zmtpHandshake(w io.Writer, r *bufio.Reader, socketType string) error {
	if _, err := w.Write(zmtpGreeting()); err != nil {
		return err
	}
	peer := make([]byte, 64)
	if _, err := io.ReadFull(r, peer); err != nil {
		return err
	}
	if peer[0] != 0xff || peer[9]&0x01 != 0x01 {
		return errors.New("peer is not a ZMTP endpoint")
	}
	if peer[10] < 3 {
		return fmt.Errorf("peer speaks ZMTP %d.x, need 3.x", peer[10])
	}
	if mech := string(bytes.TrimRight(peer[12:32], "\x00")); mech != "NULL" {
		return fmt.Errorf("unsupported security mechanism %q", mech)
	}
	// READY command with our Socket-Type.
	body := []byte{5}
	body = append(body, "READY"...)
	body = append(body, byte(len("Socket-Type")))
	body = append(body, "Socket-Type"...)
	body = binary.BigEndian.AppendUint32(body, uint32(len(socketType)))
	body = append(body, socketType...)
	if err := writeFrame(w, zmtpFlagCommand, body); err != nil {
		return err
	}
	flags, cmd, err := readFrame(r)
	if err != nil {
		return err
	}
	if flags&zmtpFlagCommand == 0 || len(cmd) < 6 || cmd[0] != 5 || string(cmd[1:6]) != "READY" {
		return errors.New("expected READY command")
	}
	props, err := parseMetadata(cmd[6:])
	if err != nil {
		return err
	}
	if st := props["socket-type"]; st != "PUB" && st != "XPUB" {
		return fmt.Errorf("peer socket type %q is not PUB", st)
	}
	return nil
}

func parseMetadata(b []byte) (map[string]string, error) {
	m := map[string]string{}
	for len(b) > 0 {
		nl := int(b[0])
		if len(b) < 1+nl+4 {
			return nil, errors.New("truncated metadata")
		}
		name := strings.ToLower(string(b[1 : 1+nl]))
		vl := int(binary.BigEndian.Uint32(b[1+nl:]))
		b = b[1+nl+4:]
		if vl < 0 || vl > len(b) {
			return nil, errors.New("truncated metadata value")
		}
		m[name] = string(b[:vl])
		b = b[vl:]
	}
	return m, nil
}

func writeFrame(w io.Writer, flags byte, body []byte) error {
	var hdr []byte
	if len(body) > 255 {
		hdr = append([]byte{flags | zmtpFlagLong}, binary.BigEndian.AppendUint64(nil, uint64(len(body)))...)
	} else {
		hdr = []byte{flags, byte(len(body))}
	}
	_, err := w.Write(append(hdr, body...))
	return err
}

func readFrame(r *bufio.Reader) (byte, []byte, error) {
	flags, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	if flags&^(zmtpFlagMore|zmtpFlagLong|zmtpFlagCommand) != 0 {
		return 0, nil, fmt.Errorf("invalid frame flags 0x%02x", flags)
	}
	var size uint64
	if flags&zmtpFlagLong != 0 {
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		size = binary.BigEndian.Uint64(b[:])
	} else {
		b, err := r.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		size = uint64(b)
	}
	if size > zmtpMaxFrame {
		return 0, nil, fmt.Errorf("frame too large: %d", size)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return flags, body, nil
}

// readMessage reads one multipart message; returns nil parts for commands.
func readMessage(r *bufio.Reader) ([][]byte, error) {
	var parts [][]byte
	for {
		flags, body, err := readFrame(r)
		if err != nil {
			return nil, err
		}
		if flags&zmtpFlagCommand != 0 {
			if len(parts) == 0 {
				return nil, nil
			}
			return nil, errors.New("command frame inside multipart message")
		}
		parts = append(parts, body)
		if len(parts) > 16 {
			return nil, errors.New("too many message parts")
		}
		if flags&zmtpFlagMore == 0 {
			return parts, nil
		}
	}
}
