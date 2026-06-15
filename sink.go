package srog

import (
	"io"
	"os"

	"github.com/rs/zerolog"
)

// Format selects how a sink serializes events.
type Format uint8

const (
	// FormatJSON writes newline-delimited JSON (NDJSON) — the machine-readable
	// form consumed by log shippers such as Fluent Bit.
	FormatJSON Format = iota
	// FormatConsole writes colorized, human-friendly lines with structured
	// parameters omitted (they remain available via a JSON sink).
	FormatConsole
)

// sinkConfig is the resolved configuration for one output destination.
type sinkConfig struct {
	format   Format
	level    Level
	levelSet bool
	noColor  bool
	rotation Rotation

	// Destination: exactly one of target (in-memory/std streams) or path (file).
	target io.Writer
	path   string
	isFile bool
}

// SinkOption customizes a single sink (console, file, or writer).
type SinkOption func(*sinkConfig)

// MinLevel sets the minimum level this sink emits, overriding the logger-wide
// level for this destination only. This is what lets a console sink show Debug
// while a file sink keeps only Warning and above.
func MinLevel(l Level) SinkOption {
	return func(s *sinkConfig) { s.level = l; s.levelSet = true }
}

// AsJSON forces NDJSON output for this sink.
func AsJSON() SinkOption { return func(s *sinkConfig) { s.format = FormatJSON } }

// AsConsole forces colorized console output for this sink.
func AsConsole() SinkOption { return func(s *sinkConfig) { s.format = FormatConsole } }

// NoColor disables ANSI colors for a console sink.
func NoColor() SinkOption { return func(s *sinkConfig) { s.noColor = true } }

// Rotate enables size/time/age-based rotation for a file sink. It has no effect
// on non-file sinks.
func Rotate(r Rotation) SinkOption { return func(s *sinkConfig) { s.rotation = r } }

// --- sink-producing logger options ---

// WithConsole adds a colorized console sink writing to os.Stdout. Use Target to
// redirect (e.g. os.Stderr) and the sink options above to tune it.
func WithConsole(opts ...SinkOption) Option {
	return func(c *config) {
		s := sinkConfig{format: FormatConsole, target: os.Stdout}
		for _, o := range opts {
			o(&s)
		}
		c.sinks = append(c.sinks, s)
	}
}

// WithFile adds a JSON file sink at path. Combine with Rotate for rotation and
// retention. The parent directory must already exist.
func WithFile(path string, opts ...SinkOption) Option {
	return func(c *config) {
		s := sinkConfig{format: FormatJSON, path: path, isFile: true}
		for _, o := range opts {
			o(&s)
		}
		c.sinks = append(c.sinks, s)
	}
}

// WithWriter adds a sink writing to an arbitrary io.Writer. It defaults to JSON;
// pass AsConsole to format it for human reading instead.
func WithWriter(w io.Writer, opts ...SinkOption) Option {
	return func(c *config) {
		s := sinkConfig{format: FormatJSON, target: w}
		for _, o := range opts {
			o(&s)
		}
		c.sinks = append(c.sinks, s)
	}
}

// build materializes the sink into its formatted writer, returning any closer
// that must be released on shutdown. The caller applies per-sink level
// filtering (see levelWriter) only when fanning out to multiple sinks.
func (s sinkConfig) build(gc *config) (io.Writer, io.Closer, error) {
	var w io.Writer
	var closer io.Closer

	switch {
	case s.isFile:
		if s.rotation.enabled() {
			rw, err := newRotatingWriter(s.path, s.rotation)
			if err != nil {
				return nil, nil, err
			}
			w, closer = rw, rw
		} else {
			f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				return nil, nil, err
			}
			w, closer = f, f
		}
	default:
		w = s.target
	}

	if s.format == FormatConsole {
		w = consoleWriter{out: w, noColor: s.noColor, showStack: gc.stack}
	}

	return w, closer, nil
}

// effectiveLevel resolves the sink's minimum level, falling back to the
// logger-wide level when the sink did not set one explicitly.
func (s sinkConfig) effectiveLevel(gc *config) Level {
	if s.levelSet {
		return s.level
	}
	return gc.level
}

// levelWriter filters events below min before delegating to the underlying
// writer, implementing zerolog.LevelWriter so MultiLevelWriter can fan out with
// independent per-sink thresholds.
type levelWriter struct {
	w   io.Writer
	min zerolog.Level
}

func (lw levelWriter) Write(p []byte) (int, error) { return lw.w.Write(p) }

func (lw levelWriter) WriteLevel(l zerolog.Level, p []byte) (int, error) {
	if l != zerolog.NoLevel && l < lw.min {
		return len(p), nil
	}
	return lw.w.Write(p)
}
