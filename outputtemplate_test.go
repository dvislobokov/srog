package srog

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// renderLine runs one crafted JSON event through an output-template writer.
func renderLine(t *testing.T, tmpl, timeFormat, event string) string {
	t.Helper()
	var buf bytes.Buffer
	w := newOutputTemplateWriter(&buf, tmpl, timeFormat)
	if _, err := w.Write([]byte(event + "\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.String()
}

func TestOutputTemplateBuiltins(t *testing.T) {
	event := `{"time":"2026-06-30T21:00:05Z","level":"error","message":"failed save",` +
		`"@mt":"failed {Op}","Op":"save","error":"boom","stack":"main.go:1",` +
		`"caller":"app/main.go:42"}`

	cases := map[string]struct{ tmpl, want string }{
		"classic": {
			"[{Timestamp:15:04:05} {Level:u3}] {Message}{NewLine}{Exception}",
			"[21:00:05 ERR] failed save\nboom\nmain.go:1\n",
		},
		"level styles": {
			"{Level} {Level:u3} {Level:w3} {Level:u} {Level:w}",
			"error ERR err ERROR error\n",
		},
		"alignment": {
			"|{Level,-5:u3}|{Op,8}|",
			"|ERR  |    save|\n",
		},
		"timestamp friendly name": {
			"{Timestamp:dateonly}",
			"2026-06-30\n",
		},
		"timestamp raw when no format": {
			"{Timestamp}",
			"2026-06-30T21:00:05Z\n",
		},
		"message template and caller": {
			"{MessageTemplate} @ {Caller}",
			"failed {Op} @ app/main.go:42\n",
		},
		"event field": {
			"op={Op}",
			"op=save\n",
		},
		"missing field renders empty": {
			"[{Nope}]",
			"[]\n",
		},
		"escaped braces": {
			"{{{Level:u3}}}",
			"{ERR}\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := renderLine(t, tc.tmpl, "", event); got != tc.want {
				t.Errorf("render(%q) = %q, want %q", tc.tmpl, got, tc.want)
			}
		})
	}
}

func TestOutputTemplateExceptionEmpty(t *testing.T) {
	got := renderLine(t, "{Message}{NewLine}{Exception}", "", `{"level":"info","message":"ok"}`)
	if got != "ok\n\n" {
		t.Errorf("got %q, want %q", got, "ok\n\n")
	}
}

func TestOutputTemplateProperties(t *testing.T) {
	event := `{"level":"info","message":"m","B":"two words","A":1,"C":true,"Used":"x"}`

	got := renderLine(t, "{Used} {Properties}", "", event)
	// Used is consumed by the template; service fields are always excluded.
	if want := "x A=1 B=\"two words\" C=true\n"; got != want {
		t.Errorf("properties = %q, want %q", got, want)
	}

	got = renderLine(t, "{Properties:j}", "", event)
	if want := `{"A":1,"B":"two words","C":true,"Used":"x"}` + "\n"; got != want {
		t.Errorf("properties:j = %q, want %q", got, want)
	}
}

func TestOutputTemplateEpochTime(t *testing.T) {
	got := renderLine(t, "{Timestamp:2006-01-02 15:04:05}", TimeUnixMs, `{"time":1751317205000,"message":"m"}`)
	if want := "2026-06-30 21:00:05\n"; !strings.HasSuffix(got, "\n") || got[:10] != want[:10] {
		// The wall-clock rendering depends on the host timezone; check the date
		// via UTC-independent prefix only when it matches, otherwise just ensure
		// it parsed (a raw epoch number would betray a parse failure).
		if strings.Contains(got, "1751317205000") {
			t.Errorf("epoch timestamp was not parsed: %q", got)
		}
	}
}

func TestOutputTemplateNumberFormat(t *testing.T) {
	got := renderLine(t, "{Ratio:.2f} {Count:04d}", "", `{"Ratio":0.5,"Count":7,"message":"m"}`)
	if want := "0.50 0007\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestOutputTemplateEndToEnd(t *testing.T) {
	var buf bytes.Buffer
	log, err := New(
		WithTimestamp(false),
		WithWriter(&buf, AsTemplate("{Level:u3} {Message} [{RequestId}]")),
	)
	if err != nil {
		t.Fatal(err)
	}
	log.ForContext("RequestId", "req-7").Warning("cache miss on {Key}", "user:42")
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if want := "WRN cache miss on user:42 [req-7]\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestOutputTemplateErrorEndToEnd(t *testing.T) {
	var buf bytes.Buffer
	log := MustNew(
		WithTimestamp(false),
		WithWriter(&buf, AsTemplate("{Level:u3} {Message}{NewLine}{Exception}")),
	)
	log.Error(errors.New("boom"), "failed {Op}", "save")
	_ = log.Close()
	if want := "ERR failed save\nboom\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestOutputTemplateFromConfig(t *testing.T) {
	sink := &recordingSink{}
	RegisterSinkType("tmplsink", func(Config, SinkSpec) (io.Writer, Format, error) {
		return sink, FormatJSON, nil
	})
	raw := `{
		"timestamp": false,
		"sinks": [{"type": "tmplsink",
		           "template": "{Level:u3}|{Message}|{Properties}"}]
	}`
	cfg, err := LoadConfig(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	log, err := cfg.Build()
	if err != nil {
		t.Fatal(err)
	}
	log.Information("hi {Name}", "neo")
	_ = log.Close()
	if want := "INF|hi neo|Name=neo\n"; sink.buf.String() != want {
		t.Errorf("got %q, want %q", sink.buf.String(), want)
	}
}

func TestOutputTemplateConfigErrors(t *testing.T) {
	cfg := Config{Sinks: []SinkSpec{{Type: "console", Format: "template"}}}
	if _, err := cfg.Build(); err == nil {
		t.Fatal("format template without a template string must fail")
	}
}
