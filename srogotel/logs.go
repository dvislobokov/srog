package srogotel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dvislobokov/srog"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
)

// defaultScopeName identifies srog as the instrumentation scope on emitted records.
const defaultScopeName = "github.com/dvislobokov/srog"

// shutdownTimeout bounds the final flush performed by Close on an owned provider.
const shutdownTimeout = 10 * time.Second

// Config configures a log Sink. The zero value is valid: it emits through the
// process-global OpenTelemetry LoggerProvider (go.opentelemetry.io/otel/log/global),
// so an application that already wired logs the same way it wired traces and
// metrics needs no further setup. Alternatively set Provider to reuse a specific
// provider, or set Endpoint to build a private OTLP exporter from the remaining
// fields. Provider and Endpoint are mutually exclusive.
type Config struct {
	// Provider emits through an existing LoggerProvider (its exporter, resource,
	// and batching settings are reused as-is). The Sink does not shut it down.
	Provider log.LoggerProvider

	// Endpoint, when set, builds a private OTLP exporter + batching provider
	// owned by the Sink (shut down and flushed by Close). Host and port only,
	// e.g. "localhost:4317" — no scheme.
	Endpoint string
	// Protocol selects the OTLP transport for Endpoint: "grpc" (default) or "http".
	Protocol string
	// Insecure disables TLS for the private exporter.
	Insecure bool
	// Headers are added to every export request (e.g. auth tokens).
	Headers map[string]string
	// Timeout bounds each export request (exporter default when zero).
	Timeout time.Duration
	// Resource describes the emitting service (service.name, ...) for the private
	// provider. Defaults to the SDK's environment-derived resource.
	Resource *resource.Resource

	// Attributes are added to every emitted record (optional). Use them for
	// routing hints the Collector reads — e.g. "data_stream.dataset" or
	// "elasticsearch.index" for the elasticsearch exporter's dynamic index. An
	// event field with the same name wins over the static value.
	Attributes map[string]string

	// ScopeName overrides the instrumentation scope name (default
	// "github.com/dvislobokov/srog").
	ScopeName string
	// TimeFormat is the layout of the logger's "time" field when it differs from
	// the default RFC3339: a Go layout, one of srog.Config's friendly names
	// (rfc3339nano, datetime, ...), or "unix", "unixms", "unixmicro", "unixnano"
	// for the epoch formats.
	TimeFormat string
	// OnError, if set, receives events that could not be translated and shutdown
	// failures. It must not log through this sink.
	OnError func(error)
}

// Sink is an io.WriteCloser that parses srog's JSON events and re-emits each one
// as an OpenTelemetry log record via the Logs Bridge API, so logs reach the OTel
// Collector through the same pipeline as traces and metrics. Construct it with
// NewSink and pass it to srog.WithWriter (JSON format, the default), or use the
// WithLogs convenience. Always Close it on shutdown so an owned provider flushes.
//
// It maps the well-known srog/zerolog fields onto the OTel Logs Data Model:
// time -> Timestamp, level -> Severity/SeverityText, message -> Body,
// trace_id/span_id -> the record's trace context, error -> "exception.message",
// stack -> "exception.stacktrace", caller -> "code.filepath"/"code.lineno",
// @mt -> "log.template"; every remaining field becomes a typed attribute.
type Sink struct {
	logger     log.Logger
	owned      *sdklog.LoggerProvider
	timeFormat string
	onError    func(error)
	// static holds Config.Attributes pre-converted; appended to every record
	// unless the event carries a field with the same name.
	static []log.KeyValue
}

// NewSink validates cfg and builds a Sink. ctx is used only to construct a
// private OTLP exporter when Endpoint is set.
func NewSink(ctx context.Context, cfg Config) (*Sink, error) {
	if cfg.Provider != nil && cfg.Endpoint != "" {
		return nil, errors.New("srogotel: Provider and Endpoint are mutually exclusive")
	}

	s := &Sink{timeFormat: resolveLayout(cfg.TimeFormat), onError: cfg.OnError}
	if len(cfg.Attributes) > 0 {
		keys := make([]string, 0, len(cfg.Attributes))
		for k := range cfg.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		s.static = make([]log.KeyValue, 0, len(keys))
		for _, k := range keys {
			s.static = append(s.static, log.String(k, cfg.Attributes[k]))
		}
	}

	provider := cfg.Provider
	if cfg.Endpoint != "" {
		exp, err := newExporter(ctx, cfg)
		if err != nil {
			return nil, err
		}
		popts := []sdklog.LoggerProviderOption{
			sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
		}
		if cfg.Resource != nil {
			popts = append(popts, sdklog.WithResource(cfg.Resource))
		}
		s.owned = sdklog.NewLoggerProvider(popts...)
		provider = s.owned
	}
	if provider == nil {
		provider = global.GetLoggerProvider()
	}

	scope := cfg.ScopeName
	if scope == "" {
		scope = defaultScopeName
	}
	s.logger = provider.Logger(scope)
	return s, nil
}

// newExporter builds the private OTLP log exporter for Endpoint.
func newExporter(ctx context.Context, cfg Config) (sdklog.Exporter, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Protocol)) {
	case "", "grpc":
		opts := []otlploggrpc.Option{otlploggrpc.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlploggrpc.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlploggrpc.WithHeaders(cfg.Headers))
		}
		if cfg.Timeout > 0 {
			opts = append(opts, otlploggrpc.WithTimeout(cfg.Timeout))
		}
		exp, err := otlploggrpc.New(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("srogotel: build OTLP/gRPC exporter: %w", err)
		}
		return exp, nil
	case "http":
		opts := []otlploghttp.Option{otlploghttp.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlploghttp.WithHeaders(cfg.Headers))
		}
		if cfg.Timeout > 0 {
			opts = append(opts, otlploghttp.WithTimeout(cfg.Timeout))
		}
		exp, err := otlploghttp.New(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("srogotel: build OTLP/HTTP exporter: %w", err)
		}
		return exp, nil
	default:
		return nil, fmt.Errorf("srogotel: unknown protocol %q (want grpc or http)", cfg.Protocol)
	}
}

// Write translates one JSON event into a log record and emits it. It always
// reports len(p) so it composes cleanly with srog's writer chain; untranslatable
// input is dropped and reported through OnError.
func (s *Sink) Write(p []byte) (int, error) {
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.UseNumber()
	var evt map[string]any
	if err := dec.Decode(&evt); err != nil {
		if s.onError != nil {
			s.onError(fmt.Errorf("srogotel: decode event: %w", err))
		}
		return len(p), nil
	}
	ctx, rec := s.record(evt)
	s.logger.Emit(ctx, rec)
	return len(p), nil
}

// Close flushes and shuts down the provider when the Sink owns one (Endpoint
// mode). A borrowed or global provider is left untouched — shut it down wherever
// it was created.
func (s *Sink) Close() error {
	if s.owned == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := s.owned.Shutdown(ctx); err != nil {
		if s.onError != nil {
			s.onError(fmt.Errorf("srogotel: shutdown provider: %w", err))
		}
		return err
	}
	return nil
}

// record maps one decoded event onto a log.Record plus the context carrying its
// trace correlation (when the event has valid trace_id/span_id fields).
func (s *Sink) record(evt map[string]any) (context.Context, log.Record) {
	var rec log.Record
	rec.SetObservedTimestamp(time.Now())

	var traceID, spanID string
	attrs := make([]log.KeyValue, 0, len(evt))

	for k, v := range evt {
		switch k {
		case zerolog.TimestampFieldName:
			if t, ok := s.parseTime(v); ok {
				rec.SetTimestamp(t)
			}
		case zerolog.LevelFieldName:
			if lvl, ok := v.(string); ok {
				num, text := severity(lvl)
				if num > 0 {
					rec.SetSeverity(num)
				}
				rec.SetSeverityText(text)
			}
		case zerolog.MessageFieldName:
			rec.SetBody(logValue(v))
		case "trace_id":
			traceID, _ = v.(string)
		case "span_id":
			spanID, _ = v.(string)
		case zerolog.ErrorFieldName:
			attrs = append(attrs, log.KeyValue{Key: "exception.message", Value: logValue(v)})
		case "stack":
			attrs = append(attrs, log.KeyValue{Key: "exception.stacktrace", Value: logValue(v)})
		case zerolog.CallerFieldName:
			attrs = append(attrs, callerAttrs(v)...)
		case "@mt":
			attrs = append(attrs, log.KeyValue{Key: "log.template", Value: logValue(v)})
		default:
			attrs = append(attrs, log.KeyValue{Key: k, Value: logValue(v)})
		}
	}

	ctx := context.Background()
	if traceID != "" && spanID != "" {
		tid, terr := trace.TraceIDFromHex(traceID)
		sid, serr := trace.SpanIDFromHex(spanID)
		if terr == nil && serr == nil {
			ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: tid,
				SpanID:  sid,
			}))
			traceID, spanID = "", ""
		}
	}
	// Unparsable IDs stay visible as plain attributes instead of vanishing.
	if traceID != "" {
		attrs = append(attrs, log.String("trace_id", traceID))
	}
	if spanID != "" {
		attrs = append(attrs, log.String("span_id", spanID))
	}

	// Static attributes fill in behind the event: a field with the same name wins.
	for _, kv := range s.static {
		if _, taken := evt[kv.Key]; !taken {
			attrs = append(attrs, kv)
		}
	}

	// Stable attribute order keeps output deterministic and tests reliable.
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].Key < attrs[j].Key })
	rec.AddAttributes(attrs...)
	return ctx, rec
}

// callerAttrs splits zerolog's "file:line" caller into the OTel code.* semantic
// convention attributes.
func callerAttrs(v any) []log.KeyValue {
	s, ok := v.(string)
	if !ok {
		return []log.KeyValue{{Key: "code.filepath", Value: logValue(v)}}
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		if line, err := strconv.ParseInt(s[i+1:], 10, 64); err == nil {
			return []log.KeyValue{
				log.String("code.filepath", s[:i]),
				log.Int64("code.lineno", line),
			}
		}
	}
	return []log.KeyValue{log.String("code.filepath", s)}
}

// logValue converts a decoded JSON value into a log.Value, preserving its type.
func logValue(v any) log.Value {
	switch x := v.(type) {
	case nil:
		return log.Value{}
	case string:
		return log.StringValue(x)
	case bool:
		return log.BoolValue(x)
	case json.Number:
		if strings.ContainsAny(x.String(), ".eE") {
			if f, err := x.Float64(); err == nil {
				return log.Float64Value(f)
			}
			return log.StringValue(x.String())
		}
		if n, err := x.Int64(); err == nil {
			return log.Int64Value(n)
		}
		return log.StringValue(x.String())
	case []any:
		vals := make([]log.Value, len(x))
		for i, e := range x {
			vals[i] = logValue(e)
		}
		return log.SliceValue(vals...)
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		kvs := make([]log.KeyValue, 0, len(x))
		for _, k := range keys {
			kvs = append(kvs, log.KeyValue{Key: k, Value: logValue(x[k])})
		}
		return log.MapValue(kvs...)
	default:
		return log.StringValue(fmt.Sprint(x))
	}
}

// severity maps a zerolog level string to the OTel log severity and its
// canonical short text. An unknown level yields (0, "").
func severity(level string) (log.Severity, string) {
	switch level {
	case "trace":
		return log.SeverityTrace, "TRACE"
	case "debug":
		return log.SeverityDebug, "DEBUG"
	case "info":
		return log.SeverityInfo, "INFO"
	case "warn":
		return log.SeverityWarn, "WARN"
	case "error":
		return log.SeverityError, "ERROR"
	case "fatal":
		return log.SeverityFatal, "FATAL"
	case "panic":
		return log.SeverityFatal4, "FATAL4"
	default:
		return 0, ""
	}
}

// parseTime interprets the "time" field: numbers according to TimeFormat's epoch
// unit (seconds unless unixms/unixmicro/unixnano), strings with the configured
// layout falling back to RFC3339(Nano).
func (s *Sink) parseTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			f, ferr := x.Float64()
			if ferr != nil {
				return time.Time{}, false
			}
			return time.Unix(0, int64(f*epochScale(s.timeFormat))), true
		}
		switch strings.ToLower(s.timeFormat) {
		case "unixnano":
			return time.Unix(0, n), true
		case "unixmicro":
			return time.UnixMicro(n), true
		case "unixms":
			return time.UnixMilli(n), true
		default: // epoch seconds
			return time.Unix(n, 0), true
		}
	case string:
		layout := s.timeFormat
		if layout == "" {
			layout = time.RFC3339
		}
		if t, err := time.Parse(layout, x); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return t, true
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}

// resolveLayout maps srog.Config's friendly time-format names to their Go
// layouts, so a config-driven sink parses timestamps the same way the logger
// writes them. Epoch markers (unix, unixms, ...) and raw Go layouts pass
// through untouched — parseTime handles those directly.
func resolveLayout(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "rfc3339":
		return time.RFC3339
	case "rfc3339nano":
		return time.RFC3339Nano
	case "datetime":
		return time.DateTime
	case "dateonly":
		return time.DateOnly
	case "timeonly":
		return time.TimeOnly
	case "kitchen":
		return time.Kitchen
	default:
		return name
	}
}

// epochScale returns nanoseconds-per-unit for a fractional epoch timestamp.
func epochScale(format string) float64 {
	switch strings.ToLower(format) {
	case "unixnano":
		return 1
	case "unixmicro":
		return 1_000
	case "unixms":
		return 1_000_000
	default:
		return 1_000_000_000
	}
}

// otlpSpec mirrors the "options" object of a `"type": "otlp"` sink entry in a
// declarative srog.Config. All fields are optional; an empty object emits
// through the process-global LoggerProvider.
type otlpSpec struct {
	Endpoint   string            `json:"endpoint" yaml:"endpoint"`
	Protocol   string            `json:"protocol" yaml:"protocol"`
	Insecure   bool              `json:"insecure" yaml:"insecure"`
	Headers    map[string]string `json:"headers" yaml:"headers"`
	Timeout    string            `json:"timeout" yaml:"timeout"` // Go duration, e.g. "10s"
	Attributes map[string]string `json:"attributes" yaml:"attributes"`
	ScopeName  string            `json:"scopeName" yaml:"scopeName"`
	TimeFormat string            `json:"timeFormat" yaml:"timeFormat"`
}

// init plugs the "otlp" sink type into srog's declarative config, so importing
// this package (even blank) enables entries like:
//
//	{"type": "otlp"}                                              // global provider
//	{"type": "otlp", "options": {"endpoint": "collector:4317",
//	                             "insecure": true}}               // private exporter
//
// The sink parses srog's JSON events, so leave the entry's format at its default.
func init() {
	srog.RegisterSinkType("otlp", func(cfg srog.Config, spec srog.SinkSpec) (io.Writer, srog.Format, error) {
		var o otlpSpec
		if err := spec.DecodeOptions(&o); err != nil {
			return nil, 0, fmt.Errorf("srogotel: otlp sink: %w", err)
		}
		var timeout time.Duration
		if o.Timeout != "" {
			d, err := time.ParseDuration(o.Timeout)
			if err != nil {
				return nil, 0, fmt.Errorf("srogotel: otlp sink: bad timeout %q: %w", o.Timeout, err)
			}
			timeout = d
		}
		// The sink must parse timestamps the way the logger writes them, so the
		// logger-wide timeFormat is inherited unless the options override it.
		tf := o.TimeFormat
		if tf == "" {
			tf = cfg.TimeFormat
		}
		sink, err := NewSink(context.Background(), Config{
			Endpoint:   o.Endpoint,
			Protocol:   o.Protocol,
			Insecure:   o.Insecure,
			Headers:    o.Headers,
			Timeout:    timeout,
			Attributes: o.Attributes,
			ScopeName:  o.ScopeName,
			TimeFormat: tf,
		})
		if err != nil {
			return nil, 0, err
		}
		return sink, srog.FormatJSON, nil
	})
}

// WithLogs is a convenience that builds a Sink and returns a srog.Option wiring
// it as a JSON writer sink, along with the Sink so the caller can Close it on
// shutdown:
//
//	// reuse the provider already configured next to traces/metrics:
//	opt, sink, err := srogotel.WithLogs(ctx, srogotel.Config{})
//	// ...or ship to a specific collector with private settings:
//	opt, sink, err := srogotel.WithLogs(ctx, srogotel.Config{
//	    Endpoint: "localhost:4317", Insecure: true,
//	})
//	if err != nil { ... }
//	defer sink.Close()
//	log := srog.MustNew(srog.WithConsole(), opt)
//
// Extra sink options (srog.MinLevel, srog.Async, ...) are applied after the JSON
// format; do not override the format — the Sink parses srog's JSON events.
func WithLogs(ctx context.Context, cfg Config, opts ...srog.SinkOption) (srog.Option, *Sink, error) {
	s, err := NewSink(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return srog.WithWriter(s, append([]srog.SinkOption{srog.AsJSON()}, opts...)...), s, nil
}
