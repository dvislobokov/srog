package srog

import (
	"context"
	"testing"
)

func TestContextRoundTrip(t *testing.T) {
	base := MustNew(WithTimestamp(false))
	enriched := base.ForContext("RequestId", "r-1")

	ctx := NewContext(context.Background(), enriched)
	if got := FromContext(ctx); got != enriched {
		t.Fatalf("FromContext returned a different logger")
	}
}

func TestFromContextFallsBackToDefault(t *testing.T) {
	if FromContext(context.Background()) != Default() {
		t.Fatal("empty context should yield the default logger")
	}
	//lint:ignore SA1012 deliberately exercising the nil-context guard
	if FromContext(nil) != Default() {
		t.Fatal("nil context should yield the default logger")
	}
}

func TestIntoContext(t *testing.T) {
	l := MustNew(WithTimestamp(false)).Named("billing")
	ctx := l.IntoContext(context.Background())
	if FromContext(ctx) != l {
		t.Fatal("IntoContext did not store the receiver")
	}
}

func TestNewID(t *testing.T) {
	a, b := NewID(), NewID()
	if len(a) != 32 {
		t.Fatalf("want 32 hex chars, got %d (%q)", len(a), a)
	}
	if a == b {
		t.Fatal("consecutive IDs should differ")
	}
}
