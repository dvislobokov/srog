package srog

import "context"

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

// IntoContext stores the receiver in ctx and returns the derived context. It is
// the fluent counterpart of NewContext:
//
//	ctx = log.ForContext("RequestId", id).IntoContext(ctx)
func (l *Logger) IntoContext(ctx context.Context) context.Context {
	return NewContext(ctx, l)
}
