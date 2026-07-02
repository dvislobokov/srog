package srog

import (
	"fmt"
	"io"
	"sync/atomic"
)

// defaultAsyncBuffer is the queue depth used when Async is given a non-positive
// size.
const defaultAsyncBuffer = 1024

// asyncWriter decouples log producers from a slow sink by handing each write to
// a background goroutine over a bounded queue. When the queue is full it drops
// the event (counting the drop) rather than blocking the caller — logging must
// never stall the application. Close drains the queue, reports any drops through
// the error handler, and closes the underlying sink.
type asyncWriter struct {
	w       io.Writer
	under   io.Closer // underlying sink closer, closed after the queue drains
	queue   chan []byte
	done    chan struct{}
	onErr   func(error)
	dropped atomic.Uint64
	closed  atomic.Bool
}

func newAsyncWriter(w io.Writer, size int, onErr func(error), under io.Closer) *asyncWriter {
	if size <= 0 {
		size = defaultAsyncBuffer
	}
	a := &asyncWriter{
		w:     w,
		under: under,
		queue: make(chan []byte, size),
		done:  make(chan struct{}),
		onErr: onErr,
	}
	go a.loop()
	return a
}

func (a *asyncWriter) loop() {
	defer close(a.done)
	for buf := range a.queue {
		if _, err := a.w.Write(buf); err != nil && a.onErr != nil {
			a.onErr(err)
		}
	}
}

func (a *asyncWriter) Write(p []byte) (int, error) {
	// zerolog reuses p once Write returns, so the bytes must be copied before
	// they are handed to the background goroutine.
	if a.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	buf := make([]byte, len(p))
	copy(buf, p)
	select {
	case a.queue <- buf:
	default:
		a.dropped.Add(1) // queue full: drop instead of blocking the caller
	}
	return len(p), nil
}

// Close stops accepting writes, drains the queue, surfaces the drop count (if
// any) through the error handler, and closes the underlying sink. It is safe to
// call once; subsequent calls are no-ops.
func (a *asyncWriter) Close() error {
	if !a.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(a.queue)
	<-a.done
	if d := a.dropped.Load(); d > 0 && a.onErr != nil {
		a.onErr(fmt.Errorf("srog: async sink dropped %d events (queue full)", d))
	}
	if a.under != nil {
		return a.under.Close()
	}
	return nil
}
