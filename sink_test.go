package srog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFanOutConsoleAndJSON(t *testing.T) {
	var console, jsonBuf bytes.Buffer
	l := MustNew(
		WithTimestamp(false),
		WithWriter(&console, AsConsole(), NoColor()),
		WithWriter(&jsonBuf, AsJSON()),
	)
	l.Information("User {Username} from {IP}", "neo", "10.0.0.1")

	// Console: rendered message, no parameter names.
	if !strings.Contains(console.String(), "User neo from 10.0.0.1") {
		t.Errorf("console missing message: %q", console.String())
	}
	if strings.Contains(console.String(), "Username") || strings.Contains(console.String(), "@mt") {
		t.Errorf("console leaked parameters: %q", console.String())
	}

	// JSON: full structured payload.
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(jsonBuf.Bytes()), &m); err != nil {
		t.Fatalf("json sink not valid json: %v (%q)", err, jsonBuf.String())
	}
	if m["Username"] != "neo" || m["IP"] != "10.0.0.1" || m["@mt"] == nil {
		t.Errorf("json sink missing structured fields: %v", m)
	}
}

func TestPerSinkLevels(t *testing.T) {
	var dbg, warn bytes.Buffer
	l := MustNew(
		WithTimestamp(false),
		WithWriter(&dbg, AsJSON(), MinLevel(DebugLevel)),
		WithWriter(&warn, AsJSON(), MinLevel(WarningLevel)),
	)
	l.Debug("d {X}", 1)
	l.Warning("w {Y}", 2)

	dbgLines := nonEmptyLines(dbg.String())
	warnLines := nonEmptyLines(warn.String())
	if len(dbgLines) != 2 {
		t.Errorf("debug sink want 2 lines, got %d: %q", len(dbgLines), dbg.String())
	}
	if len(warnLines) != 1 {
		t.Errorf("warn sink want 1 line, got %d: %q", len(warnLines), warn.String())
	}
	if len(warnLines) == 1 && !strings.Contains(warnLines[0], `"Y":2`) {
		t.Errorf("warn sink kept the wrong event: %q", warnLines[0])
	}
}

func TestFileSinkWritesJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	l, err := New(WithTimestamp(false), WithFile(path))
	if err != nil {
		t.Fatal(err)
	}
	l.Information("hello {Who}", "world")
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &m); err != nil {
		t.Fatalf("file content not json: %v (%q)", err, data)
	}
	if m["Who"] != "world" || m["level"] != "info" {
		t.Errorf("file event wrong: %v", m)
	}
}

func TestFileSinkErrorOnBadPath(t *testing.T) {
	// A path whose parent does not exist must surface an error, not panic.
	bad := filepath.Join(t.TempDir(), "missing-dir", "app.log")
	if _, err := New(WithFile(bad)); err == nil {
		t.Fatal("expected error opening file in nonexistent directory")
	}
}

func TestTimeBasedRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rot.log")

	base := time.Date(2026, 6, 15, 23, 59, 0, 0, time.UTC)
	restore := timeNow
	timeNow = func() time.Time { return base }
	defer func() { timeNow = restore }()

	w, err := newRotatingWriter(path, Rotation{Every: Daily})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("day one\n")); err != nil {
		t.Fatal(err)
	}
	// Cross midnight into the next day.
	timeNow = func() time.Time { return base.Add(2 * time.Minute) }
	if _, err := w.Write([]byte("day two\n")); err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) < 2 {
		t.Fatalf("expected a rotated file plus the active one, got %d: %v", len(entries), names(entries))
	}
}

func TestStackInOriginErrorUnaffectedAcrossSinks(t *testing.T) {
	var jsonBuf bytes.Buffer
	l := MustNew(WithTimestamp(false), WithStackTrace(true), WithWriter(&jsonBuf, AsJSON()))
	l.Error(errors.New("boom"), "failed {Op}", "x")

	var m map[string]any
	_ = json.Unmarshal(bytes.TrimSpace(jsonBuf.Bytes()), &m)
	if s, ok := m["stack"].(string); !ok || s == "" {
		t.Errorf("stack field missing/not a string in json sink: %v", m)
	}
}

func TestConsoleWriterReportsInputLength(t *testing.T) {
	// zerolog.MultiLevelWriter flags a short write unless each writer returns
	// the input length, even though the console reformats it to a different size.
	var out bytes.Buffer
	cw := consoleWriter{out: &out, noColor: true}
	in := []byte(`{"level":"info","message":"hi","X":1}` + "\n")
	n, err := cw.Write(in)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(in) {
		t.Fatalf("console writer returned n=%d, want input length %d", n, len(in))
	}
	if out.Len() == len(in) {
		t.Fatal("expected reformatted output to differ in length from input")
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			out = append(out, sc.Text())
		}
	}
	return out
}

func names(entries []os.DirEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	return out
}
