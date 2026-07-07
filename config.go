package srog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Config is a declarative, serializable description of a Logger, suitable for
// loading from a JSON or YAML file (or any source that unmarshals into it). It
// mirrors the functional Option API: every field maps to one With* option, and
// an empty/zero field leaves that option at its default. Build it programmatically
// or decode it, then call Build:
//
//	c, err := srog.LoadConfigFile("logging.json")
//	if err != nil { ... }
//	log, err := c.Build()
//
// The struct carries both `json` and `yaml` tags, so it decodes with the standard
// library or with gopkg.in/yaml.v3 without srog itself depending on a YAML parser.
type Config struct {
	// Level is the default minimum level: one of verbose, debug, information
	// (or info), warning (or warn), error, fatal. Empty means Information.
	Level string `json:"level,omitempty" yaml:"level,omitempty"`
	// Render toggles the human-readable message. Pointer so that an explicit
	// false is distinguishable from "unset" (which defaults to true).
	Render *bool `json:"render,omitempty" yaml:"render,omitempty"`
	// Caller annotates each event with the calling file and line.
	Caller bool `json:"caller,omitempty" yaml:"caller,omitempty"`
	// Timestamp adds a timestamp to each event. Pointer so an explicit false is
	// distinguishable from "unset" (which defaults to true).
	Timestamp *bool `json:"timestamp,omitempty" yaml:"timestamp,omitempty"`
	// StackTrace captures a stack when an error is logged.
	StackTrace bool `json:"stackTrace,omitempty" yaml:"stackTrace,omitempty"`
	// TimeFormat is a friendly name (rfc3339, rfc3339nano, datetime, dateonly,
	// timeonly, kitchen, unix, unixms, unixmicro, unixnano) or a raw Go layout.
	// Empty leaves the default (RFC3339).
	TimeFormat string `json:"timeFormat,omitempty" yaml:"timeFormat,omitempty"`
	// Sinks lists the output destinations. Empty yields New's default (JSON to
	// stdout).
	Sinks []SinkSpec `json:"sinks,omitempty" yaml:"sinks,omitempty"`
}

// SinkSpec is the serializable form of one sink.
type SinkSpec struct {
	// Type is "console", "file", "stdout", or "stderr". Required.
	Type string `json:"type" yaml:"type"`
	// Target selects the stream for a "console" sink: "stdout" (default) or
	// "stderr". Ignored for other types.
	Target string `json:"target,omitempty" yaml:"target,omitempty"`
	// Path is the file path for a "file" sink. Required for that type.
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
	// Level overrides the logger-wide minimum level for this sink only.
	Level string `json:"level,omitempty" yaml:"level,omitempty"`
	// Format overrides the sink's default serialization: "json", "console",
	// "ecs", or "otel" (OpenTelemetry OTLP/JSON log records).
	Format string `json:"format,omitempty" yaml:"format,omitempty"`
	// NoColor disables ANSI colors for a console sink.
	NoColor bool `json:"noColor,omitempty" yaml:"noColor,omitempty"`
	// Rotation configures rotation/retention for a "file" sink.
	Rotation *RotationSpec `json:"rotation,omitempty" yaml:"rotation,omitempty"`
}

// RotationSpec is the serializable form of Rotation. Every is a friendly cadence
// name ("", "none", "hourly", "daily") instead of the Interval enum.
type RotationSpec struct {
	MaxSizeMB  int    `json:"maxSizeMB,omitempty" yaml:"maxSizeMB,omitempty"`
	MaxBackups int    `json:"maxBackups,omitempty" yaml:"maxBackups,omitempty"`
	MaxAgeDays int    `json:"maxAgeDays,omitempty" yaml:"maxAgeDays,omitempty"`
	Compress   bool   `json:"compress,omitempty" yaml:"compress,omitempty"`
	LocalTime  bool   `json:"localTime,omitempty" yaml:"localTime,omitempty"`
	Every      string `json:"every,omitempty" yaml:"every,omitempty"`
}

// LoadConfig decodes a JSON Config from r.
func LoadConfig(r io.Reader) (Config, error) {
	var c Config
	if err := json.NewDecoder(r).Decode(&c); err != nil {
		return Config{}, fmt.Errorf("srog: decode config: %w", err)
	}
	return c, nil
}

// LoadConfigFile reads and decodes a JSON Config from path.
func LoadConfigFile(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("srog: open config: %w", err)
	}
	defer f.Close()
	return LoadConfig(f)
}

// NewFromConfig builds a Logger from an already-parsed Config. It is shorthand
// for Config.Build.
func NewFromConfig(c Config) (*Logger, error) { return c.Build() }

// NewFromConfigFile loads a JSON Config from path and builds a Logger from it.
func NewFromConfigFile(path string) (*Logger, error) {
	c, err := LoadConfigFile(path)
	if err != nil {
		return nil, err
	}
	return c.Build()
}

// Build constructs a Logger from the Config, returning an error if any field is
// invalid or a file sink cannot be opened.
func (c Config) Build() (*Logger, error) {
	opts, err := c.Options()
	if err != nil {
		return nil, err
	}
	return New(opts...)
}

// Options translates the Config into the equivalent slice of functional Options,
// in the same order the fields are declared. Useful for composing a config with
// additional programmatic options:
//
//	opts, _ := cfg.Options()
//	log, _ := srog.New(append(opts, srog.WithWriter(buf))...)
func (c Config) Options() ([]Option, error) {
	var opts []Option

	if c.Level != "" {
		lvl, err := ParseLevel(c.Level)
		if err != nil {
			return nil, err
		}
		opts = append(opts, WithLevel(lvl))
	}
	if c.Render != nil {
		opts = append(opts, WithRenderedMessage(*c.Render))
	}
	if c.Caller {
		opts = append(opts, WithCaller(true))
	}
	if c.Timestamp != nil {
		opts = append(opts, WithTimestamp(*c.Timestamp))
	}
	if c.StackTrace {
		opts = append(opts, WithStackTrace(true))
	}
	if c.TimeFormat != "" {
		opts = append(opts, WithTimeFormat(timeFormatFromName(c.TimeFormat)))
	}

	for i, s := range c.Sinks {
		o, err := s.option()
		if err != nil {
			return nil, fmt.Errorf("srog: sinks[%d]: %w", i, err)
		}
		opts = append(opts, o)
	}
	return opts, nil
}

// option translates one SinkSpec into the matching With* Option.
func (s SinkSpec) option() (Option, error) {
	var sinkOpts []SinkOption

	if s.Level != "" {
		lvl, err := ParseLevel(s.Level)
		if err != nil {
			return nil, err
		}
		sinkOpts = append(sinkOpts, MinLevel(lvl))
	}
	if s.NoColor {
		sinkOpts = append(sinkOpts, NoColor())
	}
	if s.Format != "" {
		f, err := parseFormat(s.Format)
		if err != nil {
			return nil, err
		}
		switch f {
		case FormatJSON:
			sinkOpts = append(sinkOpts, AsJSON())
		case FormatConsole:
			sinkOpts = append(sinkOpts, AsConsole())
		case FormatECS:
			sinkOpts = append(sinkOpts, AsECS())
		case FormatOTel:
			sinkOpts = append(sinkOpts, AsOTel())
		}
	}

	switch strings.ToLower(strings.TrimSpace(s.Type)) {
	case "console":
		switch strings.ToLower(strings.TrimSpace(s.Target)) {
		case "", "stdout":
			return WithConsole(sinkOpts...), nil
		case "stderr":
			// WithConsole hardcodes stdout, so route stderr through WithWriter
			// and force the console format.
			return WithWriter(os.Stderr, append([]SinkOption{AsConsole()}, sinkOpts...)...), nil
		default:
			return nil, fmt.Errorf("unknown console target %q (want stdout or stderr)", s.Target)
		}
	case "file":
		if s.Path == "" {
			return nil, errors.New("file sink requires a path")
		}
		if s.Rotation != nil {
			r, err := s.Rotation.toRotation()
			if err != nil {
				return nil, err
			}
			sinkOpts = append(sinkOpts, Rotate(r))
		}
		return WithFile(s.Path, sinkOpts...), nil
	case "stdout":
		return WithWriter(os.Stdout, sinkOpts...), nil
	case "stderr":
		return WithWriter(os.Stderr, sinkOpts...), nil
	default:
		return nil, fmt.Errorf("unknown sink type %q", s.Type)
	}
}

// toRotation converts the serializable spec into a Rotation.
func (r RotationSpec) toRotation() (Rotation, error) {
	every, err := parseInterval(r.Every)
	if err != nil {
		return Rotation{}, err
	}
	return Rotation{
		MaxSizeMB:  r.MaxSizeMB,
		MaxBackups: r.MaxBackups,
		MaxAgeDays: r.MaxAgeDays,
		Compress:   r.Compress,
		LocalTime:  r.LocalTime,
		Every:      every,
	}, nil
}

// ParseLevel resolves a Serilog-style level name (case-insensitive) to a Level.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "verbose", "trace":
		return VerboseLevel, nil
	case "debug":
		return DebugLevel, nil
	case "information", "info":
		return InformationLevel, nil
	case "warning", "warn":
		return WarningLevel, nil
	case "error":
		return ErrorLevel, nil
	case "fatal":
		return FatalLevel, nil
	default:
		return 0, fmt.Errorf("srog: unknown level %q", s)
	}
}

// parseFormat resolves a sink format name to a Format.
func parseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "json":
		return FormatJSON, nil
	case "console", "text":
		return FormatConsole, nil
	case "ecs":
		return FormatECS, nil
	case "otel", "opentelemetry", "otlp":
		return FormatOTel, nil
	default:
		return 0, fmt.Errorf("unknown format %q (want json, console, ecs, or otel)", s)
	}
}

// parseInterval resolves a rotation cadence name to an Interval.
func parseInterval(s string) (Interval, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none":
		return NoInterval, nil
	case "hourly":
		return Hourly, nil
	case "daily":
		return Daily, nil
	default:
		return 0, fmt.Errorf("unknown rotation interval %q (want hourly or daily)", s)
	}
}

// timeFormatFromName maps a friendly format name to its layout/sentinel; an
// unrecognized value is returned unchanged so callers can pass a raw Go layout.
func timeFormatFromName(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "rfc3339":
		return TimeRFC3339
	case "rfc3339nano":
		return TimeRFC3339Nano
	case "datetime":
		return TimeDateTime
	case "dateonly":
		return TimeDateOnly
	case "timeonly":
		return TimeOnly
	case "kitchen":
		return TimeKitchen
	case "unix":
		return TimeUnix
	case "unixms":
		return TimeUnixMs
	case "unixmicro":
		return TimeUnixMicro
	case "unixnano":
		return TimeUnixNano
	default:
		return s
	}
}
