package srog

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/rs/zerolog"
)

// ANSI color codes used by the console writer.
const (
	cReset   = "\x1b[0m"
	cBold    = "\x1b[1m"
	cDim     = "\x1b[2m"
	cRed     = "\x1b[31m"
	cGreen   = "\x1b[32m"
	cYellow  = "\x1b[33m"
	cBlue    = "\x1b[34m"
	cMagenta = "\x1b[35m"
	cCyan    = "\x1b[36m"
)

// consoleWriter renders each JSON log line as a clean, human-friendly console
// row: timestamp, level, and rendered message only. Structured parameters
// (template fields, ForContext values, @mt) are intentionally omitted here —
// they live in JSON output. When showStack is enabled, an attached "stack"
// field is pretty-printed beneath the message.
type consoleWriter struct {
	out       io.Writer
	noColor   bool
	showStack bool
}

func (w consoleWriter) color(s, code string) string {
	if w.noColor || code == "" {
		return s
	}
	return code + s + cReset
}

func (w consoleWriter) Write(p []byte) (int, error) {
	var evt map[string]any
	if err := json.Unmarshal(p, &evt); err != nil {
		// Not JSON we understand — pass through unchanged.
		return w.out.Write(p)
	}

	var b bytes.Buffer

	if ts, ok := evt[zerolog.TimestampFieldName].(string); ok && ts != "" {
		b.WriteString(w.color(ts, cDim))
		b.WriteByte(' ')
	}

	lvl, _ := evt[zerolog.LevelFieldName].(string)
	b.WriteString(w.levelLabel(lvl))
	b.WriteByte(' ')

	if msg, ok := evt[zerolog.MessageFieldName].(string); ok {
		b.WriteString(w.color(msg, cBold))
	}

	// Inline the error text (but not the stack) right after the message.
	if errStr, ok := evt[zerolog.ErrorFieldName].(string); ok && errStr != "" {
		b.WriteByte(' ')
		b.WriteString(w.color(errStr, cRed))
	}

	if caller, ok := evt[zerolog.CallerFieldName].(string); ok && caller != "" {
		b.WriteByte(' ')
		b.WriteString(w.color("("+caller+")", cDim))
	}

	b.WriteByte('\n')

	if w.showStack {
		w.writeStack(&b, evt[stackFieldName])
	}

	// Report the input length (not the formatted length) so that
	// zerolog.MultiLevelWriter does not mistake reformatting for a short write.
	if _, err := w.out.Write(b.Bytes()); err != nil {
		return 0, err
	}
	return len(p), nil
}

// levelLabel returns a fixed-width, colorized three-letter level tag.
func (w consoleWriter) levelLabel(level string) string {
	label, code := "???", ""
	switch level {
	case "trace":
		label, code = "TRC", cMagenta
	case "debug":
		label, code = "DBG", cCyan
	case "info":
		label, code = "INF", cGreen
	case "warn":
		label, code = "WRN", cYellow
	case "error":
		label, code = "ERR", cRed
	case "fatal":
		label, code = "FTL", cBold+cRed
	case "panic":
		label, code = "PNC", cBold+cRed
	}
	return w.color(label, code)
}

// writeStack pretty-prints a captured stack string: function names in red,
// their source locations dimmed and indented beneath.
func (w consoleWriter) writeStack(b *bytes.Buffer, raw any) {
	s, ok := raw.(string)
	if !ok || s == "" {
		return
	}
	for _, line := range strings.Split(s, "\n") {
		if loc, isLoc := strings.CutPrefix(line, "\t"); isLoc {
			b.WriteString("        ")
			b.WriteString(w.color(loc, cDim))
		} else {
			b.WriteString("    ")
			b.WriteString(w.color(line, cRed))
		}
		b.WriteByte('\n')
	}
}
