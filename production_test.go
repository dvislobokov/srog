package srog

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestFatalFlushesAndExits(t *testing.T) {
	prev := exitFunc
	var code int
	var exited bool
	exitFunc = func(c int) { code = c; exited = true }
	defer func() { exitFunc = prev }()

	var buf bytes.Buffer
	log := MustNew(WithWriter(&buf), WithTimestamp(false))
	log.Fatal(errors.New("boom"), "fatal {Why}", "disk")

	if !exited || code != 1 {
		t.Fatalf("Fatal must call exitFunc(1); exited=%v code=%d", exited, code)
	}
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("invalid json %q: %v", buf.String(), err)
	}
	if m["level"] != "fatal" || m["Why"] != "disk" {
		t.Fatalf("fatal event not written correctly: %v", m)
	}
}

func TestSamplingEveryN(t *testing.T) {
	var buf bytes.Buffer
	log := MustNew(WithWriter(&buf), WithTimestamp(false), WithLevel(DebugLevel),
		WithSampling(EveryN(3)))
	for i := 0; i < 9; i++ {
		log.Info("tick {N}", i)
	}
	lines := bytes.Count(bytes.TrimSpace(buf.Bytes()), []byte("\n")) + 1
	if buf.Len() == 0 {
		lines = 0
	}
	// BasicSampler{N:3} emits events where counter%3==1: the 1st, 4th, 7th.
	if lines != 3 {
		t.Fatalf("EveryN(3) over 9 events should emit 3, got %d (%s)", lines, buf.Bytes())
	}
}

func TestErrorHandlerInvokedOnWriteFailure(t *testing.T) {
	var got error
	log := MustNew(
		WithWriter(failingWriter{}),
		WithTimestamp(false),
		WithErrorHandler(func(err error) { got = err }),
	)
	log.Info("this write fails")
	if got == nil {
		t.Fatal("error handler was not invoked on a failing write")
	}
	if !strings.Contains(got.Error(), "disk full") {
		t.Fatalf("unexpected error surfaced: %v", got)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }
