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
	caller  bool
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
	// timeFormatSet distinguishes "no custom format" from an explicit
	// WithTimeFormat(zerolog.TimeFormatUnix), since the latter is the empty
	// string and would otherwise be indistinguishable from unset.
	timeFormatSet bool
	sampler       zerolog.Sampler
	onError       func(error)
	sinks         []sinkConfig
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

// Common timestamp layouts for WithTimeFormat. The first group are ISO/Go time
// layouts rendered as JSON strings; the epoch group re-exports zerolog's
// sentinels (emitted as JSON numbers) so callers need not import zerolog.
const (
	// TimeRFC3339 is ISO 8601 with second precision — the default layout.
	TimeRFC3339 = time.RFC3339
	// TimeRFC3339Nano is ISO 8601 with nanosecond precision.
	TimeRFC3339Nano = time.RFC3339Nano
	// TimeDateTime is "2006-01-02 15:04:05" — a human-friendly datetime.
	TimeDateTime = time.DateTime
	// TimeDateOnly is "2006-01-02".
	TimeDateOnly = time.DateOnly
	// TimeOnly is "15:04:05".
	TimeOnly = time.TimeOnly
	// TimeKitchen is "3:04PM".
	TimeKitchen = time.Kitchen

	// TimeUnix emits epoch seconds as a JSON number.
	TimeUnix = zerolog.TimeFormatUnix
	// TimeUnixMs emits epoch milliseconds as a JSON number.
	TimeUnixMs = zerolog.TimeFormatUnixMs
	// TimeUnixMicro emits epoch microseconds as a JSON number.
	TimeUnixMicro = zerolog.TimeFormatUnixMicro
	// TimeUnixNano emits epoch nanoseconds as a JSON number.
	TimeUnixNano = zerolog.TimeFormatUnixNano
)

// WithTimeFormat sets the timestamp layout for this logger's JSON output. Pass a
// Go time layout (e.g. time.RFC3339Nano) or one of the package's Time* constants
// (TimeRFC3339Nano, TimeDateTime, TimeUnix, TimeUnixMs, ...) for ISO or epoch
// timestamps without importing zerolog. The format is applied per-logger via
// a hook, so unlike setting zerolog.TimeFieldFormat directly it does not leak
// into other loggers in the process. The default, RFC3339, is ISO 8601 and
// parses cleanly in Fluent Bit with `Time_Key time`.
func WithTimeFormat(layout string) Option {
	return func(c *config) { c.timeFormat = layout; c.timeFormatSet = true }
}

// WithErrorHandler installs fn to be called when a sink's underlying Write
// fails. By default such errors are dropped (zerolog's behavior); a handler lets
// you count them, alert, or fall back to stderr. fn must be safe for concurrent
// use and should not itself log through the same logger.
func WithErrorHandler(fn func(error)) Option { return func(c *config) { c.onError = fn } }

// Sampler decides which events are emitted, for flood control. It aliases
// zerolog's Sampler so the built-in samplers below (and any custom one) compose.
type Sampler = zerolog.Sampler

// EveryN returns a sampler that emits one of every n events, regardless of
// level. n<=1 emits everything.
func EveryN(n uint32) Sampler { return &zerolog.BasicSampler{N: n} }

// BurstLimit emits up to burst events per period, then defers to next for the
// overflow (pass nil to drop everything past the burst). Use it to cap a hot log
// site without losing the first events of each window:
//
//	srog.WithSampling(srog.BurstLimit(100, time.Second, srog.EveryN(100)))
func BurstLimit(burst uint32, period time.Duration, next Sampler) Sampler {
	return &zerolog.BurstSampler{Burst: burst, Period: period, NextSampler: next}
}

// WithSampling applies s to every event before it reaches the sinks. Sampling is
// evaluated after level filtering. Combine samplers to taste; see EveryN and
// BurstLimit.
func WithSampling(s Sampler) Option { return func(c *config) { c.sampler = s } }

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
		// Surface write failures (full disk, broken pipe, ...) to the configured
		// handler instead of dropping them silently as zerolog does by default.
		if c.onError != nil {
			w = errWriter{w: w, onErr: c.onError}
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
	// The default timestamp goes through zerolog's optimized context path. A
	// custom format is applied per-logger by timeHook instead, so we never touch
	// the process-wide zerolog.TimeFieldFormat global.
	if c.stamp && !c.timeFormatSet {
		ctx = ctx.Timestamp()
	}
	// Caller is added per-event in write (with the right skip depth) rather than
	// via ctx.Caller(), whose fixed skip would point at srog's own wrapper.
	z := ctx.Logger()
	if c.stamp && c.timeFormatSet {
		z = z.Hook(timeHook{format: c.timeFormat})
	}
	if c.sampler != nil {
		z = z.Sample(c.sampler)
	}
	return &Logger{z: z, render: c.render, stack: c.stack, caller: c.caller, closers: closers}, nil
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

// WithStackTrace returns a child logger that captures (or stops capturing) a
// stack trace on logged errors. It is the per-logger counterpart of the
// WithStackTrace construction option — useful when a caller has already captured
// a stack (e.g. a panic recovery) and wants to attach it under the "stack" field
// itself without srog adding a second, less useful one from the recovery site.
func (l *Logger) WithStackTrace(on bool) *Logger {
	c := *l
	c.stack = on
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

// Fatal logs a failure, flushes all sinks, and then calls os.Exit(1). The flush
// ensures the final event reaches file/async sinks before the process dies.
func (l *Logger) Fatal(err error, tmpl string, args ...any) {
	l.write(zerolog.FatalLevel, err, tmpl, args)
}

// write is the single hot path shared by every level method.
func (l *Logger) write(level zerolog.Level, err error, tmpl string, args []any) {
	ev := l.z.WithLevel(level)
	if ev == nil {
		return // level disabled; nothing allocated downstream
	}
	if l.caller {
		if cs := callerString(); cs != "" {
			ev = ev.Str(zerolog.CallerFieldName, cs)
		}
	}
	if err != nil {
		ev = ev.Err(err)
		if l.stack {
			// captureStack strips srog's own leading frames, so the trace begins
			// at the caller regardless of any *Ctx wrapper in between.
			if stack := captureStack(); stack != "" {
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
	if level == zerolog.FatalLevel {
		// zerolog's WithLevel(FatalLevel) does not terminate (unlike its Fatal
		// helper), so we exit ourselves — flushing sinks first so the final
		// event is not lost, which a bare os.Exit would risk.
		l.flush()
		exitFunc(1)
	}
}

// exitFunc is indirected so tests can observe Fatal without terminating.
var exitFunc = os.Exit

// flush closes the logger's sinks, forcing buffered/rotating writers to drain.
// It is called on Fatal before exit; ordinary shutdown uses Close.
func (l *Logger) flush() {
	for _, c := range l.closers {
		_ = c.Close()
	}
}

// timeHook stamps each event's timestamp using a per-logger format, sidestepping
// the process-wide zerolog.TimeFieldFormat global so that loggers with different
// formats can coexist. It mirrors zerolog's handling of the Unix epoch sentinels;
// any other value is treated as a Go time layout. Because hooks run at finalize
// time, the timestamp field is appended after the message rather than before it —
// a cosmetic difference in field order that key-based consumers ignore.
type timeHook struct {
	format string
}

func (h timeHook) Run(e *zerolog.Event, _ zerolog.Level, _ string) {
	now := time.Now()
	name := zerolog.TimestampFieldName
	switch h.format {
	case zerolog.TimeFormatUnix:
		e.Int64(name, now.Unix())
	case zerolog.TimeFormatUnixMs:
		e.Int64(name, now.UnixMilli())
	case zerolog.TimeFormatUnixMicro:
		e.Int64(name, now.UnixMicro())
	case zerolog.TimeFormatUnixNano:
		e.Int64(name, now.UnixNano())
	default:
		e.Str(name, now.Format(h.format))
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
