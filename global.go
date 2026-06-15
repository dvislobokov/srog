package srog

import (
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
