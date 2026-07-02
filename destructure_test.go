package srog

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestDestructureRendersFieldNamesInMessage(t *testing.T) {
	type Person struct {
		Name string
		Age  int
		tags []string // unexported: must be skipped
	}

	var buf bytes.Buffer
	log := MustNew(WithWriter(&buf), WithTimestamp(false))
	log.Information("user {@User} logged in", Person{Name: "neo", Age: 30, tags: []string{"x"}})

	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("invalid json %q: %v", buf.String(), err)
	}
	// The rendered message must carry field names (Serilog-style), not fmt's
	// "{neo 30}".
	want := `user Person { Name: "neo", Age: 30 } logged in`
	if m["message"] != want {
		t.Fatalf("destructured message wrong:\n got %q\nwant %q", m["message"], want)
	}
	// The structured side still destructures the object as before.
	u, ok := m["User"].(map[string]any)
	if !ok || u["Name"] != "neo" || u["Age"].(float64) != 30 {
		t.Fatalf("structured User field wrong: %v", m["User"])
	}
}

func TestStringifyRendersScalar(t *testing.T) {
	var buf bytes.Buffer
	log := MustNew(WithWriter(&buf), WithTimestamp(false))
	log.Information("id {$Id}", 42)

	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["message"] != "id 42" {
		t.Fatalf("stringify message wrong: %q", m["message"])
	}
	// $ forces the string form on the structured side too.
	if m["Id"] != "42" {
		t.Fatalf("stringify should store string, got %T %v", m["Id"], m["Id"])
	}
}
