package srog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dvislobokov/srog"
)

// stackFromEvent logs one event with stack capture on and returns the captured
// "stack" string. It runs from an external package (the realistic case: the
// caller is not inside package srog).
func stackFromEvent(t *testing.T, emit func(l *srog.Logger)) string {
	t.Helper()
	var buf bytes.Buffer
	l := srog.MustNew(srog.WithWriter(&buf), srog.WithTimestamp(false), srog.WithStackTrace(true))
	emit(l)

	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("invalid json %q: %v", buf.String(), err)
	}
	s, _ := m["stack"].(string)
	if s == "" {
		t.Fatal("expected a non-empty stack")
	}
	return s
}

func assertStackTop(t *testing.T, stack, wantFn string) {
	t.Helper()
	top := stack
	if i := strings.IndexByte(stack, '\n'); i >= 0 {
		top = stack[:i]
	}
	if !strings.Contains(top, wantFn) {
		t.Fatalf("stack should start at %s, got top frame %q\nfull:\n%s", wantFn, top, stack)
	}
	// srog's own plumbing must never appear in the trace.
	if strings.Contains(stack, "(*Logger).write") || strings.Contains(stack, "srog.ErrorCtx") {
		t.Fatalf("stack leaked srog plumbing:\n%s", stack)
	}
}

// A direct Error must produce a stack that begins at the calling function.
func TestStackStartsAtCaller_DirectError(t *testing.T) {
	stack := stackFromEvent(t, func(l *srog.Logger) {
		l.Error(errors.New("boom"), "op {X}", 1)
	})
	assertStackTop(t, stack, "TestStackStartsAtCaller_DirectError")
}

// The package-level srog.ErrorCtx adds one wrapper frame; the stack must still
// begin at the caller, not at srog.ErrorCtx. This guards the fix that made
// captureStack skip srog's leading frames instead of using a fixed skip count.
func TestStackStartsAtCaller_ErrorCtx(t *testing.T) {
	stack := stackFromEvent(t, func(l *srog.Logger) {
		ctx := l.IntoContext(context.Background())
		srog.ErrorCtx(ctx, errors.New("boom"), "op {X}", 1)
	})
	assertStackTop(t, stack, "TestStackStartsAtCaller_ErrorCtx")
}
