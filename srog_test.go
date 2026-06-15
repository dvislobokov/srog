package srog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// capture builds a JSON logger over a buffer and returns the decoded event of
// the last line written by fn.
func logEvent(t *testing.T, opts []Option, fn func(l *Logger)) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	opts = append([]Option{WithWriter(&buf), WithLevel(VerboseLevel), WithTimestamp(false)}, opts...)
	l := MustNew(opts...)
	fn(l)
	if buf.Len() == 0 {
		t.Fatal("no output produced")
	}
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("invalid json %q: %v", buf.String(), err)
	}
	return m
}

func TestNamedHolesBecomeFields(t *testing.T) {
	m := logEvent(t, nil, func(l *Logger) {
		l.Information("User {Username} logged in from {IP}", "neo", "10.0.0.1")
	})
	if m["Username"] != "neo" || m["IP"] != "10.0.0.1" {
		t.Fatalf("missing structured fields: %v", m)
	}
	if m["message"] != "User neo logged in from 10.0.0.1" {
		t.Fatalf("bad rendered message: %q", m["message"])
	}
	if m["@mt"] != "User {Username} logged in from {IP}" {
		t.Fatalf("raw template not preserved: %q", m["@mt"])
	}
}

func TestTypedFields(t *testing.T) {
	m := logEvent(t, nil, func(l *Logger) {
		l.Information("count={N} ratio={R} ok={B}", 42, 3.5, true)
	})
	if m["N"].(float64) != 42 {
		t.Errorf("N: %v", m["N"])
	}
	if m["R"].(float64) != 3.5 {
		t.Errorf("R: %v", m["R"])
	}
	if m["B"].(bool) != true {
		t.Errorf("B: %v", m["B"])
	}
	if m["message"] != "count=42 ratio=3.5 ok=true" {
		t.Errorf("message: %q", m["message"])
	}
}

func TestDestructureAndStringify(t *testing.T) {
	type point struct {
		X, Y int
	}
	m := logEvent(t, nil, func(l *Logger) {
		l.Information("struct {@Obj} string {$Str}", point{1, 2}, point{1, 2})
	})
	obj, ok := m["Obj"].(map[string]any)
	if !ok || obj["X"].(float64) != 1 || obj["Y"].(float64) != 2 {
		t.Fatalf("destructured object wrong: %v", m["Obj"])
	}
	if s, ok := m["Str"].(string); !ok || s != "{1 2}" {
		t.Fatalf("stringified value wrong: %v", m["Str"])
	}
}

func TestPositionalHoles(t *testing.T) {
	m := logEvent(t, nil, func(l *Logger) {
		l.Information("{0} + {1} = {2}", 1, 2, 3)
	})
	if m["message"] != "1 + 2 = 3" {
		t.Fatalf("positional render: %q", m["message"])
	}
	if m["0"].(float64) != 1 || m["2"].(float64) != 3 {
		t.Fatalf("positional fields: %v", m)
	}
	if _, dup := m["extra_0"]; dup {
		t.Fatalf("positional arg duplicated as extra: %v", m)
	}
}

func TestEscapedBraces(t *testing.T) {
	m := logEvent(t, nil, func(l *Logger) {
		l.Information("literal {{not a hole}} and {Real}", "x")
	})
	if m["message"] != "literal {not a hole} and x" {
		t.Fatalf("escape render: %q", m["message"])
	}
	if m["Real"] != "x" {
		t.Fatalf("Real field: %v", m["Real"])
	}
}

func TestSurplusArgsBecomeExtras(t *testing.T) {
	m := logEvent(t, nil, func(l *Logger) {
		l.Information("hi {Name}", "bob", "surplus")
	})
	if m["Name"] != "bob" {
		t.Fatalf("Name: %v", m["Name"])
	}
	if m["extra_1"] != "surplus" {
		t.Fatalf("expected extra_1=surplus, got: %v", m)
	}
}

func TestMissingArgKeepsHole(t *testing.T) {
	m := logEvent(t, nil, func(l *Logger) {
		l.Information("hi {Name} and {Other}", "bob")
	})
	if m["message"] != "hi bob and {Other}" {
		t.Fatalf("missing-arg render: %q", m["message"])
	}
}

func TestErrorAttachesField(t *testing.T) {
	sentinel := errors.New("boom")
	m := logEvent(t, nil, func(l *Logger) {
		l.Error(sentinel, "failed {Op}", "save")
	})
	if m["error"] != "boom" {
		t.Fatalf("error field: %v", m["error"])
	}
	if m["Op"] != "save" || m["level"] != "error" {
		t.Fatalf("event wrong: %v", m)
	}
}

func TestForContext(t *testing.T) {
	var buf bytes.Buffer
	base := MustNew(WithWriter(&buf), WithLevel(VerboseLevel), WithTimestamp(false))
	child := base.ForContext("RequestId", "req-7")
	child.Information("handling {Path}", "/x")

	var m map[string]any
	_ = json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m)
	if m["RequestId"] != "req-7" || m["Path"] != "/x" {
		t.Fatalf("forcontext fields: %v", m)
	}

	// Base logger must be unaffected.
	buf.Reset()
	base.Information("no ctx")
	var m2 map[string]any
	_ = json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m2)
	if _, leaked := m2["RequestId"]; leaked {
		t.Fatalf("ForContext leaked into parent: %v", m2)
	}
}

func TestAlignment(t *testing.T) {
	cases := []struct {
		tmpl string
		arg  any
		want string
	}{
		{"[{V,5}]", "ab", "[   ab]"},
		{"[{V,-5}]", "ab", "[ab   ]"},
		{"[{V,2}]", "abcd", "[abcd]"},
	}
	for _, c := range cases {
		m := logEvent(t, nil, func(l *Logger) { l.Information(c.tmpl, c.arg) })
		if m["message"] != c.want {
			t.Errorf("tmpl %q: got %q want %q", c.tmpl, m["message"], c.want)
		}
	}
}

func TestTimeFormat(t *testing.T) {
	ts := time.Date(2026, 6, 15, 13, 4, 5, 0, time.UTC)
	m := logEvent(t, nil, func(l *Logger) {
		l.Information("at {T:HH:mm:ss}", ts)
	})
	if m["message"] != "at 13:04:05" {
		t.Fatalf("time format render: %q", m["message"])
	}
}

func TestLevelFiltering(t *testing.T) {
	m := logEvent(t, []Option{WithLevel(WarningLevel)}, func(l *Logger) {
		l.Debug("should be dropped {X}", 1)
		l.Warning("kept {Y}", 2)
	})
	if m["Y"].(float64) != 2 || m["level"] != "warn" {
		t.Fatalf("level filtering wrong: %v", m)
	}
}

func TestParserResilience(t *testing.T) {
	// Unterminated and invalid holes must not panic and stay literal-ish.
	m := logEvent(t, nil, func(l *Logger) {
		l.Information("oops {unclosed and {Bad-Name} done", "x")
	})
	if m["message"] == nil {
		t.Fatal("expected a message")
	}
}

func TestStackTraceInJSON(t *testing.T) {
	m := logEvent(t, []Option{WithStackTrace(true)}, func(l *Logger) {
		l.Error(errors.New("boom"), "failed {Op}", "save")
	})
	stack, ok := m["stack"].(string)
	if !ok || stack == "" {
		t.Fatalf("expected non-empty stack string, got: %v", m["stack"])
	}
	if !strings.HasPrefix(stack, "srog.TestStackTraceInJSON") {
		t.Fatalf("stack should start at caller, got: %q", stack)
	}
}

func TestNoStackWithoutError(t *testing.T) {
	m := logEvent(t, []Option{WithStackTrace(true)}, func(l *Logger) {
		l.Information("just info {X}", 1)
	})
	if _, present := m["stack"]; present {
		t.Fatalf("stack should not appear for non-error events: %v", m)
	}
}

func TestConsoleOmitsParameters(t *testing.T) {
	var buf bytes.Buffer
	l := MustNew(
		WithWriter(&buf, AsConsole(), NoColor()),
		WithLevel(VerboseLevel), WithTimestamp(false), WithStackTrace(true))
	l.Error(errors.New("connection refused"), "login failed for {Username} from {IP}", "neo", "10.0.0.1")

	out := buf.String()
	// The rendered message and error text must be present.
	for _, want := range []string{"login failed for neo from 10.0.0.1", "connection refused", "ERR"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("console output missing %q:\n%s", want, out)
		}
	}
	// Parameter names and the raw template must NOT leak into console output.
	for _, leak := range []string{"Username", "@mt", "{IP}", "IP="} {
		if bytes.Contains(buf.Bytes(), []byte(leak)) {
			t.Errorf("console output leaked param %q:\n%s", leak, out)
		}
	}
	// A pretty stack frame should be printed.
	if !bytes.Contains(buf.Bytes(), []byte("srog.TestConsoleOmitsParameters")) {
		t.Errorf("expected pretty stack in console output:\n%s", out)
	}
}

// --- benchmarks ---

func newBench(render bool) *Logger {
	return MustNew(WithWriter(io.Discard), WithRenderedMessage(render), WithTimestamp(false))
}

func BenchmarkRendered(b *testing.B) {
	l := newBench(true)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Information("User {Username} logged in from {IP} after {Ms}ms", "neo", "10.0.0.1", 42)
	}
}

func BenchmarkStructuredOnly(b *testing.B) {
	l := newBench(false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Information("User {Username} logged in from {IP} after {Ms}ms", "neo", "10.0.0.1", 42)
	}
}

func BenchmarkParseCached(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = parse("User {Username} logged in from {IP} after {Ms}ms")
	}
}
