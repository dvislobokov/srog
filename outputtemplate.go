package srog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// outputTemplateWriter renders each JSON event through a Serilog-style output
// template (see AsTemplate): literal text interleaved with placeholders, each
// supporting the same ",alignment" and ":format" specifiers as message
// templates. Built-in placeholders:
//
//	{Timestamp[:layout]}  event time; layout is a Go layout, a friendly name
//	                      (rfc3339, datetime, ...), or a .NET-style form
//	                      (HH:mm:ss); without a layout the raw field is printed
//	{Level[:u3|w3|u|w]}   u3/w3 = 3-letter INF/inf; u/w = INFORMATION/information;
//	                      bare = zerolog's name (info)
//	{Message}             the rendered message; {MessageTemplate} = raw @mt
//	{Exception}           error text, then the stack on the next line; empty
//	                      (with no padding) when the event has no error
//	{Caller}              file:line when WithCaller is on
//	{NewLine}             "\n"
//	{Properties[:j]}      every field not otherwise consumed, as k=v pairs
//	                      (:j renders one compact JSON object instead)
//
// Any other name prints that event field ({RequestId}, {Amount,10:f2}, ...);
// a field absent from the event renders as empty, like Serilog.
type outputTemplateWriter struct {
	out io.Writer
	t   *template
	// timeFormat is the logger's effective timestamp layout (a Go layout or a
	// zerolog epoch sentinel), used to parse the "time" field for {Timestamp:...}.
	timeFormat string
	// consumed marks event fields that must not appear in {Properties}: the
	// service fields behind built-in placeholders plus every field the template
	// references by name.
	consumed map[string]bool
}

// builtinHoles are template names with dedicated rendering; event fields with
// these exact names are reachable only through {Properties:j}.
var builtinFields = []string{
	zerolog.TimestampFieldName,
	zerolog.LevelFieldName,
	zerolog.MessageFieldName,
	zerolog.ErrorFieldName,
	stackFieldName,
	zerolog.CallerFieldName,
	"@mt",
}

func newOutputTemplateWriter(out io.Writer, raw, timeFormat string) outputTemplateWriter {
	t := parse(raw)
	consumed := make(map[string]bool, len(builtinFields)+len(t.tokens))
	for _, f := range builtinFields {
		consumed[f] = true
	}
	for i := range t.tokens {
		if t.tokens[i].isHole {
			consumed[t.tokens[i].fieldName()] = true
		}
	}
	return outputTemplateWriter{out: out, t: t, timeFormat: timeFormat, consumed: consumed}
}

func (w outputTemplateWriter) Write(p []byte) (int, error) {
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.UseNumber()
	var evt map[string]any
	if err := dec.Decode(&evt); err != nil {
		return w.out.Write(p) // not JSON we understand — pass through
	}

	buf := bufPool.Get().(*[]byte)
	b := (*buf)[:0]
	for i := range w.t.tokens {
		tok := &w.t.tokens[i]
		if !tok.isHole {
			b = append(b, tok.text...)
			continue
		}
		b = w.renderHole(b, tok, evt)
	}
	b = append(b, '\n')

	_, err := w.out.Write(b)
	*buf = b
	bufPool.Put(buf)
	if err != nil {
		return 0, err
	}
	// Report the input length so MultiLevelWriter does not see a short write.
	return len(p), nil
}

// renderHole appends one placeholder's value, then applies its alignment.
func (w outputTemplateWriter) renderHole(dst []byte, tok *token, evt map[string]any) []byte {
	start := len(dst)
	switch tok.name {
	case "NewLine":
		return append(dst, '\n')
	case "Timestamp":
		dst = w.appendTimestamp(dst, tok.format, evt[zerolog.TimestampFieldName])
	case "Level":
		dst = appendLevelName(dst, tok.format, evt[zerolog.LevelFieldName])
	case "Message":
		dst = appendEventValue(dst, "", evt[zerolog.MessageFieldName])
	case "MessageTemplate":
		dst = appendEventValue(dst, "", evt["@mt"])
	case "Exception":
		dst = appendException(dst, evt)
	case "Caller":
		dst = appendEventValue(dst, "", evt[zerolog.CallerFieldName])
	case "Properties":
		dst = w.appendProperties(dst, tok.format, evt)
	default:
		if v, ok := evt[tok.fieldName()]; ok {
			dst = appendEventValue(dst, tok.format, v)
		}
	}
	return padSegment(dst, start, tok.align)
}

// appendTimestamp prints the "time" field: verbatim without a format, otherwise
// parsed per the logger's time layout and re-formatted with the requested one.
func (w outputTemplateWriter) appendTimestamp(dst []byte, format string, v any) []byte {
	if v == nil {
		return dst
	}
	if format == "" {
		return appendEventValue(dst, "", v)
	}
	t, ok := parseEventTime(v, w.timeFormat)
	if !ok {
		return appendEventValue(dst, "", v) // unparsable — print as written
	}
	return t.AppendFormat(dst, goTimeLayout(timeFormatFromName(format)))
}

// parseEventTime interprets the "time" field value under the logger's layout:
// numbers according to the epoch sentinel unit (seconds by default), strings
// with the layout falling back to RFC3339(Nano).
func parseEventTime(v any, layout string) (time.Time, bool) {
	switch x := v.(type) {
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			f, ferr := x.Float64()
			if ferr != nil {
				return time.Time{}, false
			}
			return time.Unix(0, int64(f*epochScale(layout))), true
		}
		switch layout {
		case zerolog.TimeFormatUnixNano:
			return time.Unix(0, n), true
		case zerolog.TimeFormatUnixMicro:
			return time.UnixMicro(n), true
		case zerolog.TimeFormatUnixMs:
			return time.UnixMilli(n), true
		default: // TimeFormatUnix ("") — epoch seconds
			return time.Unix(n, 0), true
		}
	case string:
		l := layout
		if l == "" {
			l = time.RFC3339
		}
		if t, err := time.Parse(l, x); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// appendLevelName prints the level under the requested style: u3/w3 are
// three-letter abbreviations (INF/inf), u/w the full Serilog names
// (INFORMATION/information), bare the zerolog name as written.
func appendLevelName(dst []byte, format string, v any) []byte {
	s, _ := v.(string)
	switch format {
	case "u3":
		return append(dst, levelAbbrev(s)...)
	case "w3":
		return append(dst, strings.ToLower(levelAbbrev(s))...)
	case "u":
		return append(dst, strings.ToUpper(levelFullName(s))...)
	case "w":
		return append(dst, levelFullName(s)...)
	default:
		return append(dst, s...)
	}
}

func levelAbbrev(level string) string {
	switch level {
	case "trace":
		return "VRB"
	case "debug":
		return "DBG"
	case "info":
		return "INF"
	case "warn":
		return "WRN"
	case "error":
		return "ERR"
	case "fatal":
		return "FTL"
	case "panic":
		return "PNC"
	default:
		if len(level) > 3 {
			level = level[:3]
		}
		return strings.ToUpper(level)
	}
}

func levelFullName(level string) string {
	switch level {
	case "trace":
		return "verbose"
	case "info":
		return "information"
	case "warn":
		return "warning"
	default:
		return level
	}
}

// appendException prints the error text and, on the following line, the stack
// trace. With neither present it prints nothing.
func appendException(dst []byte, evt map[string]any) []byte {
	errText, _ := evt[zerolog.ErrorFieldName].(string)
	stack, _ := evt[stackFieldName].(string)
	dst = append(dst, errText...)
	if stack != "" {
		if errText != "" {
			dst = append(dst, '\n')
		}
		dst = append(dst, stack...)
	}
	return dst
}

// appendProperties prints every event field the template did not consume, in
// key order: "k=v k=v" by default, one compact JSON object with format "j".
func (w outputTemplateWriter) appendProperties(dst []byte, format string, evt map[string]any) []byte {
	keys := make([]string, 0, len(evt))
	for k := range evt {
		if !w.consumed[k] {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return dst
	}
	sort.Strings(keys)

	if format == "j" {
		rest := make(map[string]any, len(keys))
		for _, k := range keys {
			rest[k] = evt[k]
		}
		if b, err := json.Marshal(rest); err == nil {
			return append(dst, b...)
		}
		return dst
	}

	for i, k := range keys {
		if i > 0 {
			dst = append(dst, ' ')
		}
		dst = append(dst, k...)
		dst = append(dst, '=')
		dst = appendPropValue(dst, evt[k])
	}
	return dst
}

// appendPropValue prints one k=v value: strings are quoted only when they would
// break the pair apart (spaces, quotes, '='), everything else prints compactly.
func appendPropValue(dst []byte, v any) []byte {
	if s, ok := v.(string); ok {
		if strings.ContainsAny(s, " =\"") {
			return strconv.AppendQuote(dst, s)
		}
		return append(dst, s...)
	}
	return appendEventValue(dst, "", v)
}

// appendEventValue prints a decoded JSON value. A ":format" specifier is applied
// to numbers via fmt (e.g. {Ratio:.2f}); composite values render as compact JSON.
func appendEventValue(dst []byte, format string, v any) []byte {
	switch x := v.(type) {
	case nil:
		return dst
	case string:
		return append(dst, x...)
	case json.Number:
		if format != "" {
			if strings.ContainsAny(x.String(), ".eE") {
				if f, err := x.Float64(); err == nil {
					return fmt.Appendf(dst, "%"+format, f)
				}
			} else if n, err := x.Int64(); err == nil {
				return fmt.Appendf(dst, "%"+format, n)
			}
		}
		return append(dst, x.String()...)
	case bool:
		return strconv.AppendBool(dst, x)
	default:
		if b, err := json.Marshal(x); err == nil {
			return append(dst, b...)
		}
		return fmt.Append(dst, x)
	}
}
