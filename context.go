package srog

import (
	"context"
	"sync"
	"sync/atomic"
)

// Field is a name/value pair extracted from a context by a ContextFieldFunc.
type Field struct {
	Name  string
	Value any
}

// ContextFieldFunc pulls zero or more structured fields out of a context. It is
// how correlation data that lives in the context — OpenTelemetry trace/span IDs,
// a tenant, a deadline — is attached to logs without srog depending on those
// packages. Register implementations with AddContextField.
type ContextFieldFunc func(ctx context.Context) []Field

// contextFields holds the registered extractors as a copy-on-write slice so the
// read path (every Ctx call) is lock-free.
var (
	contextFields  atomic.Pointer[[]ContextFieldFunc]
	contextFieldMu sync.Mutex
)

// AddContextField registers fn so that Ctx and the *Ctx package helpers enrich
// every context-scoped log with the fields fn extracts. Call it once at startup
// (it is safe for concurrent use). Integrations provide ready-made extractors —
// e.g. the srogotel module registers OpenTelemetry trace_id/span_id.
func AddContextField(fn ContextFieldFunc) {
	contextFieldMu.Lock()
	defer contextFieldMu.Unlock()
	var next []ContextFieldFunc
	if old := contextFields.Load(); old != nil {
		next = append(next, *old...)
	}
	next = append(next, fn)
	contextFields.Store(&next)
}

// ctxKey is the unexported key under which a request-scoped Logger is stored in
// a context.Context. Using a private zero-size type guarantees no collision
// with keys from other packages.
type ctxKey struct{}

// NewContext returns a copy of ctx carrying l. Middleware and interceptors use
// it to propagate a request-scoped logger (already enriched with a request ID,
// service name, etc.) down the call chain.
func NewContext(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the Logger stored in ctx, or the package Default logger
// when none is present. It never returns nil, so callers can log unconditionally:
//
//	srog.FromContext(ctx).Information("processing {OrderId}", id)
func FromContext(ctx context.Context) *Logger {
	if ctx != nil {
		if l, ok := ctx.Value(ctxKey{}).(*Logger); ok && l != nil {
			return l
		}
	}
	return Default()
}

// Ctx returns the logger for ctx, enriched with any fields produced by the
// registered ContextFieldFuncs (see AddContextField). It resolves the base
// logger with FromContext (falling back to Default), so it never returns nil and
// is the idiomatic entry point for context-scoped logging:
//
//	srog.Ctx(ctx).Information("processing {OrderId}", id) // carries trace_id, etc.
//
// With no extractors registered it costs no more than FromContext.
func Ctx(ctx context.Context) *Logger {
	l := FromContext(ctx)
	fns := contextFields.Load()
	if ctx == nil || fns == nil || len(*fns) == 0 {
		return l
	}
	var fields map[string]any
	for _, fn := range *fns {
		for _, f := range fn(ctx) {
			if fields == nil {
				fields = make(map[string]any)
			}
			fields[f.Name] = f.Value
		}
	}
	if fields == nil {
		return l
	}
	return l.ForContextValues(fields)
}

// IntoContext stores the receiver in ctx and returns the derived context. It is
// the fluent counterpart of NewContext:
//
//	ctx = log.ForContext("RequestId", id).IntoContext(ctx)
func (l *Logger) IntoContext(ctx context.Context) context.Context {
	return NewContext(ctx, l)
}
