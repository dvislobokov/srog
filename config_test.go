package srog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigBuildFromJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	raw := `{
		"level": "debug",
		"timestamp": false,
		"stackTrace": true,
		"timeFormat": "unixms",
		"sinks": [
			{"type": "file", "path": "` + filepath.ToSlash(path) + `", "level": "warning",
			 "rotation": {"maxSizeMB": 5, "maxBackups": 3, "every": "daily"}}
		]
	}`

	cfg, err := LoadConfig(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	log, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	log.Debug("dropped {X}", 1)         // below the sink's warning threshold
	log.Warning("kept {Y}", 2)          // emitted
	if err := log.Close(); err != nil { // flush rotation buffers before reading
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("expected exactly one line (warning), got %d: %s", len(lines), data)
	}
	var m map[string]any
	if err := json.Unmarshal(lines[0], &m); err != nil {
		t.Fatalf("invalid json %q: %v", lines[0], err)
	}
	if m["level"] != "warn" || m["Y"].(float64) != 2 {
		t.Fatalf("unexpected event: %v", m)
	}
	// timestamp:false means no time field at all.
	if _, ok := m["time"]; ok {
		t.Fatalf("timestamp should be disabled, got: %v", m["time"])
	}
}

func TestConfigOptionsErrors(t *testing.T) {
	cases := map[string]Config{
		"bad level":       {Level: "loud"},
		"bad sink type":   {Sinks: []SinkSpec{{Type: "carrier-pigeon"}}},
		"file no path":    {Sinks: []SinkSpec{{Type: "file"}}},
		"bad sink level":  {Sinks: []SinkSpec{{Type: "console", Level: "screaming"}}},
		"bad format":      {Sinks: []SinkSpec{{Type: "console", Format: "xml"}}},
		"bad interval":    {Sinks: []SinkSpec{{Type: "file", Path: "x", Rotation: &RotationSpec{Every: "weekly"}}}},
		"bad console tgt": {Sinks: []SinkSpec{{Type: "console", Target: "printer"}}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := cfg.Build(); err == nil {
				t.Fatalf("expected error for %q, got nil", name)
			}
		})
	}
}

func TestParseLevelRoundTrip(t *testing.T) {
	for in, want := range map[string]Level{
		"VERBOSE": VerboseLevel,
		"info":    InformationLevel,
		"warn":    WarningLevel,
		" Error ": ErrorLevel,
	} {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Fatalf("ParseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

func TestConfigRenderDefaultsOn(t *testing.T) {
	// Render unset (nil) must keep the default of true: the message renders.
	var buf bytes.Buffer
	opts, err := Config{Level: "info"}.Options()
	if err != nil {
		t.Fatalf("Options: %v", err)
	}
	log := MustNew(append(opts, WithWriter(&buf), WithTimestamp(false))...)
	log.Information("hi {Name}", "neo")

	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["message"] != "hi neo" {
		t.Fatalf("render should default on, message = %q", m["message"])
	}
}
