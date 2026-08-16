package servelog

import (
	"io"
	"log"
	"os"
	"strings"
	"testing"
)

func TestBufferRing(t *testing.T) {
	b := New(3)
	_, _ = b.Write([]byte("l1\n"))
	_, _ = b.Write([]byte("l2\n"))
	_, _ = b.Write([]byte("l3\n"))
	_, _ = b.Write([]byte("l4\n"))
	got := b.Tail(0)
	if len(got) != 3 || got[0] != "l2\n" || got[2] != "l4\n" {
		t.Fatalf("ring buffer wrong: %q", got)
	}
	if b.Len() != 3 {
		t.Fatalf("len=%d, want 3", b.Len())
	}
	tail := b.Tail(2)
	if len(tail) != 2 || tail[0] != "l3\n" || tail[1] != "l4\n" {
		t.Fatalf("Tail(2) wrong: %q", tail)
	}
	b.Clear()
	if b.Len() != 0 {
		t.Fatalf("len after clear = %d", b.Len())
	}
}

func TestBufferTailEmpty(t *testing.T) {
	b := New(10)
	if got := b.Tail(5); got != nil && len(got) != 0 {
		t.Fatalf("empty tail = %q", got)
	}
}

func TestBufferThroughLog(t *testing.T) {
	b := New(10)
	old := log.Writer()
	log.SetOutput(io.MultiWriter(os.Stderr, b))
	log.Print("server test line")
	log.SetOutput(old)
	joined := strings.Join(b.Tail(0), "")
	if !strings.Contains(joined, "server test line") {
		t.Fatalf("log line not captured: %q", joined)
	}
}
