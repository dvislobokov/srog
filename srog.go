// Package srog is a structured logger built on zerolog that speaks Serilog's
// message-template language. Named holes in a template become typed structured
// fields, while an optional human-readable message is rendered alongside them:
//
//	log := srog.MustNew(srog.WithConsole())
//	log.Information("User {Username} logged in from {IP}", "neo", "10.0.0.1")
//
// A logger fans out to any number of sinks, each with its own format and level —
// for example pretty console plus rotated JSON files that Fluent Bit can tail:
//
//	log, err := srog.New(
//	    srog.WithConsole(srog.MinLevel(srog.DebugLevel)),
//	    srog.WithFile("/var/log/app.log",
//	        srog.MinLevel(srog.InformationLevel),
//	        srog.Rotate(srog.Rotation{MaxSizeMB: 100, MaxBackups: 10, Compress: true, Every: srog.Daily}),
//	    ),
//	)
//	defer log.Close()
//
// The API mirrors Serilog (Verbose/Debug/Information/Warning/Error/Fatal and
// ForContext) while keeping zerolog's zero-allocation event model underneath.
package srog

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
)

// Level mirrors Serilog's severity ladder. Values map onto zerolog levels.
type Level int8

const (
	VerboseLevel     Level = Level(zerolog.TraceLevel)
	DebugLevel       Level = Level(zerolog.DebugLevel)
	InformationLevel Level = Level(zerolog.InfoLevel)
	WarningLevel     Level = Level(zerolog.WarnLevel)
	ErrorLevel       Level = Level(zerolog.ErrorLevel)
	FatalLevel       Level = Level(zerolog.FatalLevel)
)

// Logger is an immutable, concurrency-safe logger. Derive enriched loggers with
// ForContext; the zero value is not usable — construct one with New/MustNew.
type Logger struct {
	z       zerolog.Logger
	render  bool
	stack   bool
	closers []io.Closer
}

// Option customizes a Logger at construction time.
type Option func(*config)

type config struct {
	level      Level
	render     bool
	caller     bool
	stamp      bool
	stack      bool
	timeFormat string
	sinks      []sinkConfig
}

// WithLevel sets the default minimum level emitted by sinks that do not specify
// their own via MinLevel.
func WithLevel(l Level) Option { return func(c *config) { c.level = l } }

// WithRenderedMessage toggles rendering of the human-readable message into the
// "message" field. Disable it for maximum throughput when you only consume the
// structured fields plus the raw template. Enabled by default. Console sinks
// rely on the rendered message, so keep it on when using them.
func WithRenderedMessage(on bool) Option { return func(c *config) { c.render = on } }

// WithCaller annotates each event with the calling file and line.
func WithCaller(on bool) Option { return func(c *config) { c.caller = on } }

// WithTimestamp adds a timestamp to each event. Enabled by default.
func WithTimestamp(on bool) Option { return func(c *config) { c.stamp = on } }

// WithStackTrace captures a call stack whenever an error is logged via Error or
// Fatal. The stack is stored in the structured "stack" field (always present in
// JSON output) and pretty-printed by console sinks.
func WithStackTrace(on bool) Option { return func(c *config) { c.stack = on } }

// WithTimeFormat sets the timestamp layout for all JSON output. Pass a Go time
// layout (e.g. time.RFC3339Nano) or one of zerolog's sentinels
// (zerolog.TimeFormatUnix, TimeFormatUnixMs, TimeFormatUnixMicro) for epoch
// timestamps. Note: zerolog stores this process-wide, so the last logger
// constructed wins. The default, RFC3339, is ISO 8601 and parses cleanly in
// Fluent Bit with `Time_Key time`.
func WithTimeFormat(layout string) Option { return func(c *config) { c.timeFormat = layout } }

// New constructs a Logger from the given options. With no sink option it
// defaults to a single JSON sink on os.Stdout at Information level. It returns
// an error if a file sink cannot be opened.
func New(opts ...Option) (*Logger, error) {
	c := config{level: InformationLevel, render: true, stamp: true}
	for _, o := range opts {
		o(&c)
	}
	if len(c.sinks) == 0 {
		c.sinks = []sinkConfig{{format: FormatJSON, target: os.Stdout}}
	}
	if c.timeFormat != "" {
		zerolog.TimeFieldFormat = c.timeFormat
	}

	writers := make([]io.Writer, 0, len(c.sinks))
	closers := make([]io.Closer, 0, len(c.sinks))
	minLevel := FatalLevel
	single := len(c.sinks) == 1
	for _, s := range c.sinks {
		w, closer, err := s.build(&c)
		if err != nil {
			for _, cl := range closers {
				_ = cl.Close()
			}
			return nil, err
		}
		if closer != nil {
			closers = append(closers, closer)
		}
		eff := s.effectiveLevel(&c)
		if eff < minLevel {
			minLevel = eff
		}
		// With multiple sinks each needs its own threshold; with a single sink
		// the logger's own level already filters, so skip the wrapper.
		if single {
			writers = append(writers, w)
		} else {
			writers = append(writers, levelWriter{w: w, min: zerolog.Level(eff)})
		}
	}

	// Avoid the MultiLevelWriter fan-out cost when there is a single sink.
	var out io.Writer
	if single {
		out = writers[0]
	} else {
		out = zerolog.MultiLevelWriter(writers...)
	}
	ctx := zerolog.New(out).Level(zerolog.Level(minLevel)).With()
	if c.stamp {
		ctx = ctx.Timestamp()
	}
	if c.caller {
		ctx = ctx.Caller()
	}
	return &Logger{z: ctx.Logger(), render: c.render, stack: c.stack, closers: closers}, nil
}

// MustNew is like New but panics on error. Use it for configurations that
// cannot fail (no file sinks) or when a startup failure should abort the
// process.
func MustNew(opts ...Option) *Logger {
	l, err := New(opts...)
	if err != nil {
		panic(err)
	}
	return l
}

// NewConsole is a convenience constructor for development: colorized console
// output at Debug level with pretty stack traces on errors.
func NewConsole() *Logger {
	return MustNew(WithConsole(), WithLevel(DebugLevel), WithStackTrace(true))
}

// Close releases any file sinks held by the logger. Call it once on the root
// logger during shutdown; derived (ForContext) loggers share the same sinks.
func (l *Logger) Close() error {
	var firstErr error
	for _, c := range l.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ForContext returns a child logger that includes the given property on every
// event, equivalent to Serilog's ForContext. The receiver is left unchanged.
func (l *Logger) ForContext(name string, value any) *Logger {
	c := *l
	c.z = bindCtx(l.z.With(), name, value).Logger()
	return &c
}

// Named returns a child logger tagged with a "service" property, the idiomatic
// way to identify which component or service emitted an event:
//
//	svcLog := log.Named("billing")
//	svcLog.Information("charged {Amount}", 999) // every event carries service=billing
func (l *Logger) Named(service string) *Logger {
	return l.ForContext("service", service)
}

// ForContextValues returns a child logger enriched with several properties.
func (l *Logger) ForContextValues(fields map[string]any) *Logger {
	ctx := l.z.With()
	for k, v := range fields {
		ctx = bindCtx(ctx, k, v)
	}
	c := *l
	c.z = ctx.Logger()
	return &c
}

// WithLevel returns a child logger with a different minimum level.
func (l *Logger) WithLevel(level Level) *Logger {
	c := *l
	c.z = l.z.Level(zerolog.Level(level))
	return &c
}

// Enabled reports whether events at level would be emitted by this logger.
func (l *Logger) Enabled(level Level) bool {
	return zerolog.Level(level) >= l.z.GetLevel()
}

// --- Serilog-style level methods ---

// Verbose logs at the lowest severity (maps to zerolog Trace).
func (l *Logger) Verbose(tmpl string, args ...any) { l.write(zerolog.TraceLevel, nil, tmpl, args) }

// Debug logs diagnostic detail useful during development.
func (l *Logger) Debug(tmpl string, args ...any) { l.write(zerolog.DebugLevel, nil, tmpl, args) }

// Information logs a normal, expected event.
func (l *Logger) Information(tmpl string, args ...any) {
	l.write(zerolog.InfoLevel, nil, tmpl, args)
}

// Info is a shorthand alias for Information.
func (l *Logger) Info(tmpl string, args ...any) { l.write(zerolog.InfoLevel, nil, tmpl, args) }

// Warning logs a recoverable concern.
func (l *Logger) Warning(tmpl string, args ...any) { l.write(zerolog.WarnLevel, nil, tmpl, args) }

// Error logs a failure. Pass the triggering error as err; it is attached under
// the standard "error" field. Use nil when there is no associated error.
func (l *Logger) Error(err error, tmpl string, args ...any) {
	l.write(zerolog.ErrorLevel, err, tmpl, args)
}

// Fatal logs a failure and then calls os.Exit(1), matching zerolog semantics.
func (l *Logger) Fatal(err error, tmpl string, args ...any) {
	l.write(zerolog.FatalLevel, err, tmpl, args)
}

// write is the single hot path shared by every level method.
func (l *Logger) write(level zerolog.Level, err error, tmpl string, args []any) {
	ev := l.z.WithLevel(level)
	if ev == nil {
		return // level disabled; nothing allocated downstream
	}
	if err != nil {
		ev = ev.Err(err)
		if l.stack {
			// Skip captureStack, write, and the level method so the trace
			// begins at the caller's frame.
			if stack := captureStack(3); stack != "" {
				ev = ev.Str(stackFieldName, stack)
			}
		}
	}
	t := parse(tmpl)
	// Preserve the raw template for structured consumers, as Serilog does.
	ev = ev.Str("@mt", t.raw)
	msg := t.apply(ev, l.render, args)
	if l.render {
		ev.Msg(msg)
	} else {
		ev.Send()
	}
}

// bindCtx attaches a value to a zerolog context, mirroring bindField's typed
// dispatch but for the persistent (With) builder.
func bindCtx(ctx zerolog.Context, key string, val any) zerolog.Context {
	switch v := val.(type) {
	case string:
		return ctx.Str(key, v)
	case bool:
		return ctx.Bool(key, v)
	case int:
		return ctx.Int(key, v)
	case int64:
		return ctx.Int64(key, v)
	case uint64:
		return ctx.Uint64(key, v)
	case float64:
		return ctx.Float64(key, v)
	case time.Time:
		return ctx.Time(key, v)
	case time.Duration:
		return ctx.Dur(key, v)
	case error:
		return ctx.AnErr(key, v)
	default:
		return ctx.Interface(key, v)
	}
}
