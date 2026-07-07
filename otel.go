package srog

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// otelWriter rewrites each zerolog JSON line into a single OpenTelemetry log
// record encoded as OTLP/JSON (one LogRecord per line, NDJSON). This is the
// representation the OpenTelemetry Collector's otlpjson receiver and the file
// exporter understand, so events flow into any OTel logs pipeline
// (Collector -> Loki/Elastic/etc.) without a translation step.
//
// It maps the well-known zerolog fields onto the OpenTelemetry Logs Data Model:
//
//	time    -> timeUnixNano / observedTimeUnixNano
//	level   -> severityNumber + severityText
//	message -> body.stringValue
//	trace_id/span_id -> traceId/spanId (promoted out of attributes)
//	error   -> attribute "exception.message"
//	stack   -> attribute "exception.stacktrace"
//	caller  -> attributes "code.filepath" + "code.lineno"
//	@mt     -> attribute "log.template"
//
// Every remaining field (template holes, ForContext values, ...) becomes an
// attribute with an OTLP AnyValue that preserves its JSON type. It decodes with
// json.Number so integers keep full precision across the re-encode.
type otelWriter struct {
	out io.Writer
	// timeFormat is the logger's effective timestamp layout, used to interpret
	// the "time" field and convert it to Unix nanoseconds. It is one of the
	// zerolog epoch sentinels (TimeFormatUnix/…Ms/…Micro/…Nano) or a Go layout.
	timeFormat string
}

func (w otelWriter) Write(p []byte) (int, error) {
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.UseNumber()
	var evt map[string]any
	if err := dec.Decode(&evt); err != nil {
		return w.out.Write(p) // not JSON we understand — pass through
	}

	rec := make(map[string]any, 8)
	attrs := make([]map[string]any, 0, len(evt))

	for k, v := range evt {
		switch k {
		case zerolog.TimestampFieldName:
			if nanos, ok := w.toUnixNano(v); ok {
				rec["timeUnixNano"] = nanos
				rec["observedTimeUnixNano"] = nanos
			}
		case zerolog.LevelFieldName:
			if s, ok := v.(string); ok {
				num, text := otelSeverity(s)
				if num > 0 {
					rec["severityNumber"] = num
				}
				rec["severityText"] = text
			}
		case zerolog.MessageFieldName:
			rec["body"] = otelAnyValue(v)
		case "trace_id":
			if s, ok := v.(string); ok {
				rec["traceId"] = s
			}
		case "span_id":
			if s, ok := v.(string); ok {
				rec["spanId"] = s
			}
		case zerolog.ErrorFieldName:
			attrs = append(attrs, otelAttr("exception.message", v))
		case stackFieldName:
			attrs = append(attrs, otelAttr("exception.stacktrace", v))
		case zerolog.CallerFieldName:
			// "file:line" -> OTel code.* semantic-convention attributes.
			if s, ok := v.(string); ok {
				if i := strings.LastIndexByte(s, ':'); i >= 0 {
					if _, err := strconv.ParseInt(s[i+1:], 10, 64); err == nil {
						attrs = append(attrs, otelAttr("code.filepath", s[:i]))
						attrs = append(attrs, otelAttr("code.lineno", json.Number(s[i+1:])))
					} else {
						attrs = append(attrs, otelAttr("code.filepath", s))
					}
				} else {
					attrs = append(attrs, otelAttr("code.filepath", s))
				}
			} else {
				attrs = append(attrs, otelAttr("code.filepath", v))
			}
		case "@mt":
			attrs = append(attrs, otelAttr("log.template", v))
		default:
			attrs = append(attrs, otelAttr(k, v))
		}
	}

	// Stable attribute order keeps the NDJSON diff-friendly and tests reliable.
	sort.Slice(attrs, func(i, j int) bool {
		return attrs[i]["key"].(string) < attrs[j]["key"].(string)
	})
	if len(attrs) > 0 {
		rec["attributes"] = attrs
	}

	b, err := json.Marshal(rec)
	if err != nil {
		return w.out.Write(p)
	}
	b = append(b, '\n')
	if _, err := w.out.Write(b); err != nil {
		return 0, err
	}
	// Report the input length so MultiLevelWriter does not see a short write.
	return len(p), nil
}

// otelAttr builds one OTLP key/value attribute entry.
func otelAttr(key string, v any) map[string]any {
	return map[string]any{"key": key, "value": otelAnyValue(v)}
}

// otelAnyValue encodes v as an OTLP AnyValue, preserving its JSON type. Integers
// are emitted as decimal strings under intValue per the proto3-JSON int64
// mapping; floats as doubleValue; and nested slices/maps recurse into
// arrayValue/kvlistValue.
func otelAnyValue(v any) map[string]any {
	switch x := v.(type) {
	case nil:
		return map[string]any{}
	case string:
		return map[string]any{"stringValue": x}
	case bool:
		return map[string]any{"boolValue": x}
	case json.Number:
		s := x.String()
		if strings.ContainsAny(s, ".eE") {
			if f, err := x.Float64(); err == nil {
				return map[string]any{"doubleValue": f}
			}
			return map[string]any{"stringValue": s}
		}
		return map[string]any{"intValue": s}
	case float64:
		return map[string]any{"doubleValue": x}
	case int:
		return map[string]any{"intValue": strconv.Itoa(x)}
	case int64:
		return map[string]any{"intValue": strconv.FormatInt(x, 10)}
	case []any:
		vals := make([]map[string]any, len(x))
		for i, e := range x {
			vals[i] = otelAnyValue(e)
		}
		return map[string]any{"arrayValue": map[string]any{"values": vals}}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		vals := make([]map[string]any, 0, len(x))
		for _, k := range keys {
			vals = append(vals, otelAttr(k, x[k]))
		}
		return map[string]any{"kvlistValue": map[string]any{"values": vals}}
	default:
		return map[string]any{"stringValue": toString(x)}
	}
}

// otelSeverity maps a zerolog level string to an OpenTelemetry SeverityNumber
// and its canonical short SeverityText. An unknown level yields (0, "").
func otelSeverity(level string) (int, string) {
	switch level {
	case "trace":
		return 1, "TRACE"
	case "debug":
		return 5, "DEBUG"
	case "info":
		return 9, "INFO"
	case "warn":
		return 13, "WARN"
	case "error":
		return 17, "ERROR"
	case "fatal":
		return 21, "FATAL"
	case "panic":
		return 24, "FATAL4"
	default:
		return 0, ""
	}
}

// toUnixNano converts the "time" field into an OTLP timeUnixNano string (decimal
// nanoseconds since the Unix epoch). It interprets numeric values according to
// the logger's epoch time format and parses string values with the configured
// layout, falling back to RFC3339(Nano). It returns ok=false when the value
// cannot be interpreted, so the record simply omits the timestamp.
func (w otelWriter) toUnixNano(v any) (string, bool) {
	switch x := v.(type) {
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			f, ferr := x.Float64()
			if ferr != nil {
				return "", false
			}
			n = int64(f * epochScale(w.timeFormat))
			return strconv.FormatInt(n, 10), true
		}
		switch w.timeFormat {
		case zerolog.TimeFormatUnixNano:
			return strconv.FormatInt(n, 10), true
		case zerolog.TimeFormatUnixMicro:
			return strconv.FormatInt(n*1_000, 10), true
		case zerolog.TimeFormatUnixMs:
			return strconv.FormatInt(n*1_000_000, 10), true
		default: // TimeFormatUnix ("") — epoch seconds
			return strconv.FormatInt(n*1_000_000_000, 10), true
		}
	case string:
		layout := w.timeFormat
		if layout == "" {
			layout = time.RFC3339
		}
		if t, err := time.Parse(layout, x); err == nil {
			return strconv.FormatInt(t.UnixNano(), 10), true
		}
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return strconv.FormatInt(t.UnixNano(), 10), true
		}
		return "", false
	default:
		return "", false
	}
}

// epochScale returns the nanoseconds-per-unit factor for a fractional epoch
// timestamp under the given format.
func epochScale(format string) float64 {
	switch format {
	case zerolog.TimeFormatUnixNano:
		return 1
	case zerolog.TimeFormatUnixMicro:
		return 1_000
	case zerolog.TimeFormatUnixMs:
		return 1_000_000
	default:
		return 1_000_000_000
	}
}
