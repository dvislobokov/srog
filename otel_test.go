package srog

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
)

// decodeOTel unmarshals one OTLP/JSON log record and returns it plus its
// attributes flattened into a name->AnyValue map for convenient assertions.
func decodeOTel(t *testing.T, b []byte) (map[string]any, map[string]map[string]any) {
	t.Helper()
	var rec map[string]any
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(b)))
	dec.UseNumber()
	if err := dec.Decode(&rec); err != nil {
		t.Fatalf("invalid json %q: %v", b, err)
	}
	attrs := map[string]map[string]any{}
	if raw, ok := rec["attributes"].([]any); ok {
		for _, a := range raw {
			m := a.(map[string]any)
			attrs[m["key"].(string)] = m["value"].(map[string]any)
		}
	}
	return rec, attrs
}

func TestOTelFieldMapping(t *testing.T) {
	var buf bytes.Buffer
	log := MustNew(
		WithWriter(&buf, AsOTel()),
		WithStackTrace(true),
		WithCaller(true),
		WithTimeFormat(TimeRFC3339),
	)
	log.Error(errors.New("boom"), "failed {Op} for {UserId}", "save", 4242)

	rec, attrs := decodeOTel(t, buf.Bytes())

	// Severity is promoted to the OTel data-model fields.
	if rec["severityText"] != "ERROR" {
		t.Fatalf("severityText = %v, want ERROR", rec["severityText"])
	}
	if rec["severityNumber"] != json.Number("17") {
		t.Fatalf("severityNumber = %v, want 17", rec["severityNumber"])
	}

	// Body carries the rendered message as an AnyValue.
	body, ok := rec["body"].(map[string]any)
	if !ok || body["stringValue"] != "failed save for 4242" {
		t.Fatalf("body = %v, want stringValue=failed save for 4242", rec["body"])
	}

	// timeUnixNano must be present and a valid decimal integer string.
	ts, ok := rec["timeUnixNano"].(string)
	if !ok {
		t.Fatalf("timeUnixNano missing or not a string: %T %v", rec["timeUnixNano"], rec["timeUnixNano"])
	}
	if _, err := strconv.ParseInt(ts, 10, 64); err != nil {
		t.Fatalf("timeUnixNano %q not an integer: %v", ts, err)
	}
	if rec["observedTimeUnixNano"] != ts {
		t.Fatalf("observedTimeUnixNano = %v, want %v", rec["observedTimeUnixNano"], ts)
	}

	// Error and stack map to the exception.* semantic conventions.
	if v := attrs["exception.message"]; v["stringValue"] != "boom" {
		t.Fatalf("exception.message = %v, want boom", v)
	}
	if v := attrs["exception.stacktrace"]; v == nil || v["stringValue"] == "" {
		t.Fatalf("missing exception.stacktrace attribute")
	}

	// Caller maps to code.filepath (string) + code.lineno (int).
	if v := attrs["code.filepath"]; v["stringValue"] == nil || v["stringValue"] == "" {
		t.Fatalf("missing code.filepath: %v", v)
	}
	if v := attrs["code.lineno"]; v["intValue"] == nil {
		t.Fatalf("missing/invalid code.lineno: %v", v)
	}

	// Template hole UserId (int) keeps full precision as an intValue string.
	if v := attrs["UserId"]; v["intValue"] != "4242" {
		t.Fatalf("UserId = %v, want intValue=4242", v)
	}
	// String hole Op stays a stringValue.
	if v := attrs["Op"]; v["stringValue"] != "save" {
		t.Fatalf("Op = %v, want stringValue=save", v)
	}
	// The message template survives as an attribute.
	if v := attrs["log.template"]; v["stringValue"] != "failed {Op} for {UserId}" {
		t.Fatalf("log.template = %v", v)
	}

	// Raw zerolog keys must not leak into the record.
	for _, leaked := range []string{"time", "level", "message"} {
		if _, ok := rec[leaked]; ok {
			t.Fatalf("raw zerolog %q leaked into OTel output", leaked)
		}
	}
}

// The unix-epoch time formats must be scaled up to nanoseconds.
func TestOTelUnixTimeFormats(t *testing.T) {
	cases := map[string]string{
		TimeUnix:      "", // seconds -> *1e9
		TimeUnixMs:    "ms",
		TimeUnixMicro: "us",
		TimeUnixNano:  "ns",
	}
	for format := range cases {
		var buf bytes.Buffer
		log := MustNew(WithWriter(&buf, AsOTel()), WithTimeFormat(format))
		log.Information("tick")

		rec, _ := decodeOTel(t, buf.Bytes())
		ts, ok := rec["timeUnixNano"].(string)
		if !ok {
			t.Fatalf("format %q: timeUnixNano missing: %v", format, rec["timeUnixNano"])
		}
		n, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			t.Fatalf("format %q: timeUnixNano %q not integer: %v", format, ts, err)
		}
		// A current timestamp in nanoseconds is a 19-digit number; anything much
		// smaller means the epoch unit was not scaled to nanoseconds.
		if n < 1_000_000_000_000_000_000 {
			t.Fatalf("format %q: timeUnixNano %d not scaled to nanoseconds", format, n)
		}
	}
}

// Trace correlation fields are promoted to traceId/spanId, not left as attributes.
func TestOTelTraceCorrelation(t *testing.T) {
	var buf bytes.Buffer
	log := MustNew(WithWriter(&buf, AsOTel()), WithTimestamp(false))
	log.ForContext("trace_id", "abc123").ForContext("span_id", "def456").
		Information("hi")

	rec, attrs := decodeOTel(t, buf.Bytes())
	if rec["traceId"] != "abc123" {
		t.Fatalf("traceId = %v, want abc123", rec["traceId"])
	}
	if rec["spanId"] != "def456" {
		t.Fatalf("spanId = %v, want def456", rec["spanId"])
	}
	if _, leaked := attrs["trace_id"]; leaked {
		t.Fatalf("trace_id should be promoted to traceId, not left as an attribute")
	}
}
