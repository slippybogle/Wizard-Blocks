package node

import (
	"bufio"
	"bytes"
	"testing"
)

func TestZMTPFrames(t *testing.T) {
	var buf bytes.Buffer
	long := bytes.Repeat([]byte{7}, 300)
	_ = writeFrame(&buf, zmtpFlagMore, []byte("hashblock"))
	_ = writeFrame(&buf, zmtpFlagMore, long)
	_ = writeFrame(&buf, 0, []byte{1, 0, 0, 0})
	_ = writeFrame(&buf, zmtpFlagCommand, []byte("\x04PING"))
	r := bufio.NewReader(&buf)
	parts, err := readMessage(r)
	if err != nil || len(parts) != 3 || string(parts[0]) != "hashblock" || !bytes.Equal(parts[1], long) {
		t.Fatalf("parts %v err %v", parts, err)
	}
	parts, err = readMessage(r)
	if err != nil || parts != nil {
		t.Fatalf("command frame: %v %v", parts, err)
	}
	if _, err := readMessage(r); err == nil {
		t.Fatal("expected EOF")
	}
	// Oversized and invalid frames are rejected without allocation blowups.
	bad := bufio.NewReader(bytes.NewReader([]byte{zmtpFlagLong, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}))
	if _, _, err := readFrame(bad); err == nil {
		t.Fatal("oversized frame accepted")
	}
	if _, _, err := readFrame(bufio.NewReader(bytes.NewReader([]byte{0x80, 0}))); err == nil {
		t.Fatal("bad flags accepted")
	}
}

func TestZMTPMetadata(t *testing.T) {
	m, err := parseMetadata([]byte("\x0bSocket-Type\x00\x00\x00\x03PUB"))
	if err != nil || m["socket-type"] != "PUB" {
		t.Fatal(m, err)
	}
	if _, err := parseMetadata([]byte("\x0bSocket-Type\x00\x00\x00\x09PUB")); err == nil {
		t.Fatal("truncated value accepted")
	}
}
