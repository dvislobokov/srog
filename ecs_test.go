package srog

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestECSFieldMapping(t *testing.T) {
	var buf bytes.Buffer
	log := MustNew(
		WithWriter(&buf, AsECS()),
		WithStackTrace(true),
		WithCaller(true),
		WithTimeFormat(TimeRFC3339),
	)
	log.Error(errors.New("boom"), "failed {Op} for {UserId}", "save", 4242)

	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(buf.Bytes())))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("invalid json %q: %v", buf.String(), err)
	}

	// ECS-renamed fields.
	checks := map[string]any{
		"ecs.version":           ecsVersion,
		"log.level":             "error",
		"error.message":         "boom",
		"message":               "failed save for 4242",
		"message_template.text": "failed {Op} for {UserId}",
	}
	for k, want := range checks {
		if m[k] != want {
			t.Fatalf("ECS field %q = %v, want %v", k, m[k], want)
		}
	}
	if _, ok := m["@timestamp"]; !ok {
		t.Fatal("missing @timestamp")
	}
	if m["error.stack_trace"] == nil || m["error.stack_trace"] == "" {
		t.Fatal("missing error.stack_trace")
	}
	if m["log.origin.file.name"] == nil {
		t.Fatal("missing log.origin.file.name")
	}
	// log.origin.file.line must be a preserved integer.
	if _, ok := m["log.origin.file.line"].(json.Number); !ok {
		t.Fatalf("log.origin.file.line not numeric: %T %v", m["log.origin.file.line"], m["log.origin.file.line"])
	}
	// Template field UserId (int) must survive with full precision, not the old
	// zerolog "time"/"level" keys.
	if m["UserId"] != json.Number("4242") {
		t.Fatalf("UserId lost/renamed: %v", m["UserId"])
	}
	if _, leaked := m["time"]; leaked {
		t.Fatal("raw zerolog 'time' field leaked into ECS output")
	}
}
