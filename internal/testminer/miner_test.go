package testminer

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"net"
	"strings"
	"testing"
	"time"
)

func notifyLine(id string) string {
	prev := strings.Repeat("00", 32)
	return fmt.Sprintf(`{"id":null,"method":"mining.notify","params":["%s","%s","01","02",[],"20000000","207fffff","6500000a",false]}`+"\n", id, prev)
}

// The pool sends mining.set_difficulty and then mining.notify as two lines.
// A test that sees the new difficulty in State and immediately reads the
// latest job can still get the old one (regtest loop 2, run 15:
// "difficulty change was not followed by a fresh job"). WaitJobOtherThan
// must wait for the job instead.
func TestNewDifficultyVisibleBeforeItsJob(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sendDiff := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		if _, err := r.ReadString('\n'); err != nil { // mining.subscribe, id 1
			return
		}
		fmt.Fprint(conn, `{"id":1,"result":[[],"00000001",4],"error":null}`+"\n")
		fmt.Fprint(conn, notifyLine("1"))
		<-sendDiff
		fmt.Fprint(conn, `{"id":null,"method":"mining.set_difficulty","params":[0.5]}`+"\n")
		time.Sleep(300 * time.Millisecond) // the gap the old test fell into
		fmt.Fprint(conn, notifyLine("2"))
		time.Sleep(time.Second)
	}()

	c, err := Dial(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Subscribe(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := c.WaitJob(ctx)
	if err != nil || first.ID != "1" {
		t.Fatalf("first job %v %v", first, err)
	}
	close(sendDiff)
	for {
		if _, _, _, d, _ := c.State(); math.Abs(d-0.5) < 1e-12 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	// The old check: the latest job right after the difficulty is seen.
	if j := c.DrainJobs(); j.ID != "1" {
		t.Fatalf("expected the old job to still be latest (reproduction), got %s", j.ID)
	}
	j, err := c.WaitJobOtherThan(ctx, first.ID)
	if err != nil || j.ID != "2" {
		t.Fatalf("WaitJobOtherThan: %v %v", j, err)
	}
}
