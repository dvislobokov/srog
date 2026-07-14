package srogotel_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/srogotel"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/log/logtest"
	"go.opentelemetry.io/otel/trace"
)

// records flattens the recorder's result into the single expected scope.
func records(t *testing.T, rec *logtest.Recorder) []logtest.Record {
	t.Helper()
	var out []logtest.Record
	for _, rs := range rec.Result() {
		out = append(out, rs...)
	}
	return out
}

func attr(rs []log.KeyValue, key string) (log.Value, bool) {
	for _, kv := range rs {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return log.Value{}, false
}

func TestSinkMapsEvent(t *testing.T) {
	rec := logtest.NewRecorder()
	sink, err := srogotel.NewSink(context.Background(), srogotel.Config{Provider: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	line := `{"level":"error","time":"2026-06-30T21:00:00Z","message":"failed save",` +
		`"@mt":"failed {Op}","Op":"save","error":"boom","stack":"goroutine 1 ...",` +
		`"caller":"app/main.go:42","Count":7,"Ratio":0.5,"Ok":true}`
	if _, err := sink.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}

	got := records(t, rec)
	if len(got) != 1 {
		t.Fatalf("records = %d, want 1", len(got))
	}
	r := got[0]

	if r.Severity != log.SeverityError || r.SeverityText != "ERROR" {
		t.Errorf("severity = %v %q, want SeverityError ERROR", r.Severity, r.SeverityText)
	}
	if want := time.Date(2026, 6, 30, 21, 0, 0, 0, time.UTC); !r.Timestamp.Equal(want) {
		t.Errorf("timestamp = %v, want %v", r.Timestamp, want)
	}
	if r.ObservedTimestamp.IsZero() {
		t.Error("observed timestamp is zero")
	}
	if r.Body.AsString() != "failed save" {
		t.Errorf("body = %q, want %q", r.Body.AsString(), "failed save")
	}

	for _, tc := range []struct {
		key  string
		want log.Value
	}{
		{"log.template", log.StringValue("failed {Op}")},
		{"Op", log.StringValue("save")},
		{"exception.message", log.StringValue("boom")},
		{"exception.stacktrace", log.StringValue("goroutine 1 ...")},
		{"code.filepath", log.StringValue("app/main.go")},
		{"code.lineno", log.Int64Value(42)},
		{"Count", log.Int64Value(7)},
		{"Ratio", log.Float64Value(0.5)},
		{"Ok", log.BoolValue(true)},
	} {
		v, ok := attr(r.Attributes, tc.key)
		if !ok {
			t.Errorf("attribute %q missing", tc.key)
			continue
		}
		if !v.Equal(tc.want) {
			t.Errorf("attribute %q = %v, want %v", tc.key, v, tc.want)
		}
	}
}

func TestSinkPromotesTraceContext(t *testing.T) {
	rec := logtest.NewRecorder()
	sink, err := srogotel.NewSink(context.Background(), srogotel.Config{Provider: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	line := `{"level":"info","message":"hi",` +
		`"trace_id":"0123456789abcdef0123456789abcdef","span_id":"0123456789abcdef"}`
	if _, err := sink.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}

	got := records(t, rec)
	if len(got) != 1 {
		t.Fatalf("records = %d, want 1", len(got))
	}
	sc := trace.SpanContextFromContext(got[0].Context)
	if sc.TraceID().String() != "0123456789abcdef0123456789abcdef" {
		t.Errorf("trace id = %s", sc.TraceID())
	}
	if sc.SpanID().String() != "0123456789abcdef" {
		t.Errorf("span id = %s", sc.SpanID())
	}
	if _, ok := attr(got[0].Attributes, "trace_id"); ok {
		t.Error("trace_id should be promoted out of attributes")
	}
}

func TestSinkKeepsInvalidTraceIDAsAttribute(t *testing.T) {
	rec := logtest.NewRecorder()
	sink, _ := srogotel.NewSink(context.Background(), srogotel.Config{Provider: rec})
	defer sink.Close()

	if _, err := sink.Write([]byte(`{"message":"hi","trace_id":"nope","span_id":"also-nope"}`)); err != nil {
		t.Fatal(err)
	}
	got := records(t, rec)
	if v, ok := attr(got[0].Attributes, "trace_id"); !ok || v.AsString() != "nope" {
		t.Errorf("trace_id attribute = %v, %v", v, ok)
	}
}

func TestSinkReportsBadInput(t *testing.T) {
	rec := logtest.NewRecorder()
	var reported error
	sink, _ := srogotel.NewSink(context.Background(), srogotel.Config{
		Provider: rec,
		OnError:  func(err error) { reported = err },
	})
	defer sink.Close()

	n, err := sink.Write([]byte("not json\n"))
	if err != nil || n != len("not json\n") {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if reported == nil {
		t.Error("decode error was not reported")
	}
	if len(records(t, rec)) != 0 {
		t.Error("bad input should not produce a record")
	}
}

func TestSinkUsesGlobalProviderByDefault(t *testing.T) {
	rec := logtest.NewRecorder()
	prev := global.GetLoggerProvider()
	global.SetLoggerProvider(rec)
	defer global.SetLoggerProvider(prev)

	sink, err := srogotel.NewSink(context.Background(), srogotel.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	if _, err := sink.Write([]byte(`{"level":"info","message":"via global"}`)); err != nil {
		t.Fatal(err)
	}
	got := records(t, rec)
	if len(got) != 1 || got[0].Body.AsString() != "via global" {
		t.Fatalf("global provider did not receive the record: %+v", got)
	}
}

func TestNewSinkRejectsProviderAndEndpoint(t *testing.T) {
	_, err := srogotel.NewSink(context.Background(), srogotel.Config{
		Provider: logtest.NewRecorder(),
		Endpoint: "localhost:4317",
	})
	if err == nil {
		t.Fatal("want error for Provider+Endpoint")
	}
}

func TestNewSinkRejectsUnknownProtocol(t *testing.T) {
	_, err := srogotel.NewSink(context.Background(), srogotel.Config{
		Endpoint: "localhost:4317",
		Protocol: "carrier-pigeon",
	})
	if err == nil {
		t.Fatal("want error for unknown protocol")
	}
}

func TestWithLogsEndToEnd(t *testing.T) {
	rec := logtest.NewRecorder()
	opt, sink, err := srogotel.WithLogs(context.Background(), srogotel.Config{Provider: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	logger, err := srog.New(opt, srog.WithTimestamp(false))
	if err != nil {
		t.Fatal(err)
	}
	logger.Information("charged {Amount} cents", 999)
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}

	got := records(t, rec)
	if len(got) != 1 {
		t.Fatalf("records = %d, want 1", len(got))
	}
	r := got[0]
	if r.Severity != log.SeverityInfo {
		t.Errorf("severity = %v, want info", r.Severity)
	}
	if v, ok := attr(r.Attributes, "Amount"); !ok || !v.Equal(log.Int64Value(999)) {
		t.Errorf("Amount attribute = %v, %v", v, ok)
	}
	if v, ok := attr(r.Attributes, "log.template"); !ok || v.AsString() != "charged {Amount} cents" {
		t.Errorf("log.template = %v, %v", v, ok)
	}
}

func TestSinkStaticAttributes(t *testing.T) {
	rec := logtest.NewRecorder()
	sink, err := srogotel.NewSink(context.Background(), srogotel.Config{
		Provider: rec,
		Attributes: map[string]string{
			"data_stream.dataset": "billing",
			"Region":              "static-region",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	// The event's own Region must win over the static one.
	if _, err := sink.Write([]byte(`{"message":"hi","Region":"eu-west-1"}`)); err != nil {
		t.Fatal(err)
	}

	got := records(t, rec)
	if len(got) != 1 {
		t.Fatalf("records = %d, want 1", len(got))
	}
	if v, ok := attr(got[0].Attributes, "data_stream.dataset"); !ok || v.AsString() != "billing" {
		t.Errorf("data_stream.dataset = %v, %v", v, ok)
	}
	var regions []string
	for _, kv := range got[0].Attributes {
		if kv.Key == "Region" {
			regions = append(regions, kv.Value.AsString())
		}
	}
	if len(regions) != 1 || regions[0] != "eu-west-1" {
		t.Errorf("Region = %v, want exactly [eu-west-1]", regions)
	}
}

func TestOTLPSinkTypeFromConfig(t *testing.T) {
	rec := logtest.NewRecorder()
	prev := global.GetLoggerProvider()
	global.SetLoggerProvider(rec)
	defer global.SetLoggerProvider(prev)

	// No endpoint in options -> the sink emits through the global provider.
	cfg, err := srog.LoadConfig(strings.NewReader(`{
		"timeFormat": "unixms",
		"sinks": [{"type": "otlp", "options": {
			"scopeName": "cfg-test",
			"attributes": {"data_stream.dataset": "billing"}}}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	logger, err := cfg.Build()
	if err != nil {
		t.Fatal(err)
	}
	logger.Information("built from {Source}", "config")
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}

	var got []logtest.Record
	for scope, rs := range rec.Result() {
		if scope.Name != "cfg-test" {
			t.Errorf("scope = %q, want cfg-test", scope.Name)
		}
		got = append(got, rs...)
	}
	if len(got) != 1 {
		t.Fatalf("records = %d, want 1", len(got))
	}
	r := got[0]
	if r.Body.AsString() != "built from config" {
		t.Errorf("body = %q", r.Body.AsString())
	}
	// timeFormat "unixms" was inherited from the logger-wide config.
	if r.Timestamp.IsZero() || time.Since(r.Timestamp) > time.Minute {
		t.Errorf("timestamp not parsed from unixms field: %v", r.Timestamp)
	}
	if v, ok := attr(r.Attributes, "Source"); !ok || v.AsString() != "config" {
		t.Errorf("Source attribute = %v, %v", v, ok)
	}
	if v, ok := attr(r.Attributes, "data_stream.dataset"); !ok || v.AsString() != "billing" {
		t.Errorf("static attribute from config = %v, %v", v, ok)
	}
}

func TestOTLPSinkTypeRejectsBadOptions(t *testing.T) {
	for name, raw := range map[string]string{
		"bad timeout":  `{"sinks":[{"type":"otlp","options":{"timeout":"soon"}}]}`,
		"bad protocol": `{"sinks":[{"type":"otlp","options":{"endpoint":"x:1","protocol":"pigeon"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := srog.LoadConfig(strings.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := cfg.Build(); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestSinkParsesEpochTime(t *testing.T) {
	rec := logtest.NewRecorder()
	sink, _ := srogotel.NewSink(context.Background(), srogotel.Config{
		Provider:   rec,
		TimeFormat: "unixms",
	})
	defer sink.Close()

	if _, err := sink.Write([]byte(`{"time":1751317200000,"message":"epoch"}`)); err != nil {
		t.Fatal(err)
	}
	got := records(t, rec)
	if want := time.UnixMilli(1751317200000); !got[0].Timestamp.Equal(want) {
		t.Errorf("timestamp = %v, want %v", got[0].Timestamp, want)
	}
}
