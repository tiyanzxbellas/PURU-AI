// Package servelog keeps a bounded in-memory tail of the process logs so the
// web dashboard can show a live "Server Logs" console (9router console-log
// style). main.go installs a MultiWriter so the standard log package feeds both
// stderr and this ring buffer.
package servelog

import "sync"

// Buffer is a concurrency-safe ring buffer of log lines.
type Buffer struct {
	mu    sync.Mutex
	lines []string
	cap   int
}

// New returns a Buffer that keeps at most cap lines.
func New(cap int) *Buffer {
	if cap <= 0 {
		cap = 1000
	}
	return &Buffer{cap: cap}
}

// Write appends p as a single line entry (used as an io.Writer by log).
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, string(p))
	if len(b.lines) > b.cap {
		b.lines = b.lines[len(b.lines)-b.cap:]
	}
	return len(p), nil
}

// Tail returns the n most recent lines in order (or all stored when n<=0).
func (b *Buffer) Tail(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > len(b.lines) {
		out := make([]string, len(b.lines))
		copy(out, b.lines)
		return out
	}
	out := make([]string, n)
	copy(out, b.lines[len(b.lines)-n:])
	return out
}

// Clear empties the buffer.
func (b *Buffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = b.lines[:0]
}

// Len returns the number of buffered lines.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}
