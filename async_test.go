package srog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAsyncDeliversAllEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "async.log")

	log := MustNew(
		WithFile(path, Async(0)), // default buffer, comfortably larger than N
		WithTimestamp(false),
		WithLevel(DebugLevel),
	)
	const n = 200
	for i := 0; i < n; i++ {
		log.Info("event {N}", i)
	}
	if err := log.Close(); err != nil { // drains the queue and closes the file
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := bytes.Count(bytes.TrimSpace(data), []byte("\n")) + 1
	if got != n {
		t.Fatalf("async sink lost events: wrote %d, file has %d", n, got)
	}
}

// gatedWriter blocks in Write until release is closed, letting the test fill the
// async queue and force drops deterministically.
type gatedWriter struct {
	release chan struct{}
	written int
}

func (g *gatedWriter) Write(p []byte) (int, error) {
	<-g.release
	g.written += len(p)
	return len(p), nil
}

func TestAsyncDropsReportedOnClose(t *testing.T) {
	gate := &gatedWriter{release: make(chan struct{})}
	var dropErr error
	log := MustNew(
		WithWriter(gate, Async(2)), // tiny queue
		WithTimestamp(false),
		WithErrorHandler(func(err error) { dropErr = err }),
	)

	// The background goroutine blocks on the first Write; flooding past the
	// 2-slot queue forces drops.
	for i := 0; i < 100; i++ {
		log.Info("flood {N}", i)
	}
	close(gate.release) // unblock the writer so Close can drain and finish
	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if dropErr == nil {
		t.Fatal("expected a drop report through the error handler")
	}
	if !bytes.Contains([]byte(dropErr.Error()), []byte("dropped")) {
		t.Fatalf("unexpected error: %v", dropErr)
	}
}
