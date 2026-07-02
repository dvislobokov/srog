package srog

import (
	"context"
	"os"
	"sync/atomic"
)

// global holds the package-level default logger, swappable atomically so that
// SetDefault is safe to call concurrently with logging.
var global atomic.Pointer[Logger]

func init() {
	global.Store(MustNew(WithWriter(os.Stdout)))
}

// Default returns the current package-level logger.
func Default() *Logger { return global.Load() }

// SetDefault replaces the package-level logger used by the top-level functions.
func SetDefault(l *Logger) { global.Store(l) }

// Package-level convenience functions mirror Serilog's static Log facade and
// delegate to the Default logger.

func Verbose(tmpl string, args ...any) { global.Load().Verbose(tmpl, args...) }

func Debug(tmpl string, args ...any) { global.Load().Debug(tmpl, args...) }

func Information(tmpl string, args ...any) { global.Load().Information(tmpl, args...) }

func Info(tmpl string, args ...any) { global.Load().Info(tmpl, args...) }

func Warning(tmpl string, args ...any) { global.Load().Warning(tmpl, args...) }

func Error(err error, tmpl string, args ...any) { global.Load().Error(err, tmpl, args...) }

func Fatal(err error, tmpl string, args ...any) { global.Load().Fatal(err, tmpl, args...) }

// ForContext derives an enriched logger from the package default.
func ForContext(name string, value any) *Logger { return global.Load().ForContext(name, value) }

// Context-first convenience functions. Each resolves the logger with Ctx — so it
// carries both the request-scoped fields (RequestId, ...) and anything the
// registered ContextFieldFuncs extract (trace_id, ...) — and logs through it,
// without the handler keeping a local logger variable:
//
//	srog.InfoCtx(ctx, "charged {Amount}", 999)

func VerboseCtx(ctx context.Context, tmpl string, args ...any) {
	Ctx(ctx).Verbose(tmpl, args...)
}

func DebugCtx(ctx context.Context, tmpl string, args ...any) {
	Ctx(ctx).Debug(tmpl, args...)
}

func InformationCtx(ctx context.Context, tmpl string, args ...any) {
	Ctx(ctx).Information(tmpl, args...)
}

// InfoCtx is a shorthand alias for InformationCtx.
func InfoCtx(ctx context.Context, tmpl string, args ...any) {
	Ctx(ctx).Info(tmpl, args...)
}

func WarningCtx(ctx context.Context, tmpl string, args ...any) {
	Ctx(ctx).Warning(tmpl, args...)
}

func ErrorCtx(ctx context.Context, err error, tmpl string, args ...any) {
	Ctx(ctx).Error(err, tmpl, args...)
}

func FatalCtx(ctx context.Context, err error, tmpl string, args ...any) {
	Ctx(ctx).Fatal(err, tmpl, args...)
}
