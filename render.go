package srog

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// bufPool reuses byte buffers for message rendering to avoid per-log allocations.
var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 256)
		return &b
	},
}

// apply binds template holes to args: each hole becomes a typed structured field
// on ev, and (when render is true) the human-readable message is built and
// returned. Surplus args beyond the holes are attached as "extra_N" fields so no
// data is silently dropped.
func (t *template) apply(ev *zerolog.Event, render bool, args []any) string {
	if !t.hasHoles {
		// Pure-text template: every arg is surplus context.
		bindExtras(ev, 0, args)
		return t.raw
	}

	var buf *[]byte
	if render {
		buf = bufPool.Get().(*[]byte)
		*buf = (*buf)[:0]
	}

	used := 0
	for i := range t.tokens {
		tok := &t.tokens[i]
		if !tok.isHole {
			if render {
				*buf = append(*buf, tok.text...)
			}
			continue
		}

		// Resolve the argument feeding this hole.
		var val any
		var ok bool
		if tok.kind == holePositional {
			if tok.pos < len(args) {
				val, ok = args[tok.pos], true
			}
			if tok.pos+1 > used {
				used = tok.pos + 1
			}
		} else {
			if tok.rawIndex < len(args) {
				val, ok = args[tok.rawIndex], true
			}
			if tok.rawIndex+1 > used {
				used = tok.rawIndex + 1
			}
		}

		name := tok.fieldName()
		if ok {
			bindField(ev, name, tok.capture, val)
			if render {
				*buf = renderValue(*buf, tok, val)
			}
		} else if render {
			// Missing argument: echo the raw hole, like Serilog.
			*buf = append(*buf, '{')
			*buf = append(*buf, tok.name...)
			*buf = append(*buf, '}')
		}
	}

	// Attach any positional surplus that the template didn't reference.
	bindExtras(ev, used, args)

	if !render {
		return t.raw
	}
	msg := string(*buf)
	bufPool.Put(buf)
	return msg
}

// fieldName returns the structured-field key for a hole.
func (tok *token) fieldName() string {
	if tok.kind == holePositional {
		return strconv.Itoa(tok.pos)
	}
	return tok.name
}

// bindExtras adds positional args not consumed by named holes as extra_N fields.
func bindExtras(ev *zerolog.Event, used int, args []any) {
	for i := used; i < len(args); i++ {
		bindField(ev, "extra_"+strconv.Itoa(i), captureDefault, args[i])
	}
}

// bindField attaches val to ev under key, choosing the most specific zerolog
// method for val's dynamic type. The type switch covers the common hot path
// without reflection; uncommon types fall through to Interface/Str.
func bindField(ev *zerolog.Event, key string, cap capture, val any) {
	if val == nil {
		ev.Interface(key, nil)
		return
	}

	switch cap {
	case captureStringify:
		ev.Str(key, toString(val))
		return
	case captureDestructure:
		ev.Interface(key, val)
		return
	}

	switch v := val.(type) {
	case string:
		ev.Str(key, v)
	case []byte:
		ev.Bytes(key, v)
	case bool:
		ev.Bool(key, v)
	case int:
		ev.Int(key, v)
	case int8:
		ev.Int8(key, v)
	case int16:
		ev.Int16(key, v)
	case int32:
		ev.Int32(key, v)
	case int64:
		ev.Int64(key, v)
	case uint:
		ev.Uint(key, v)
	case uint8:
		ev.Uint8(key, v)
	case uint16:
		ev.Uint16(key, v)
	case uint32:
		ev.Uint32(key, v)
	case uint64:
		ev.Uint64(key, v)
	case float32:
		ev.Float32(key, v)
	case float64:
		ev.Float64(key, v)
	case time.Time:
		ev.Time(key, v)
	case time.Duration:
		ev.Dur(key, v)
	case error:
		ev.AnErr(key, v)
	case fmt.Stringer:
		ev.Str(key, v.String())
	default:
		// Structs, maps, slices: let zerolog serialize as a JSON value.
		ev.Interface(key, v)
	}
}

// renderValue appends the formatted scalar representation of val to dst,
// honoring alignment and (best-effort) format specifiers.
func renderValue(dst []byte, tok *token, val any) []byte {
	start := len(dst)
	dst = appendValue(dst, tok.format, val)

	// Apply alignment by padding the just-written segment.
	if tok.align != 0 {
		written := len(dst) - start
		pad := tok.align
		left := pad < 0
		if left {
			pad = -pad
		}
		if written < pad {
			gap := pad - written
			if left {
				for i := 0; i < gap; i++ {
					dst = append(dst, ' ')
				}
			} else {
				// Right-align: shift segment and prepend spaces.
				dst = append(dst, make([]byte, gap)...)
				copy(dst[start+gap:], dst[start:start+written])
				for i := 0; i < gap; i++ {
					dst[start+i] = ' '
				}
			}
		}
	}
	return dst
}

// appendValue writes a scalar's text form to dst without intermediate strings
// for the common types. A non-empty format falls back to fmt for fidelity.
func appendValue(dst []byte, format string, val any) []byte {
	if format != "" {
		if tv, ok := val.(time.Time); ok {
			return tv.AppendFormat(dst, goTimeLayout(format))
		}
		return append(dst, fmt.Sprintf("%"+format, val)...)
	}
	switch v := val.(type) {
	case string:
		return append(dst, v...)
	case bool:
		return strconv.AppendBool(dst, v)
	case int:
		return strconv.AppendInt(dst, int64(v), 10)
	case int64:
		return strconv.AppendInt(dst, v, 10)
	case int32:
		return strconv.AppendInt(dst, int64(v), 10)
	case uint:
		return strconv.AppendUint(dst, uint64(v), 10)
	case uint64:
		return strconv.AppendUint(dst, v, 10)
	case float64:
		return strconv.AppendFloat(dst, v, 'g', -1, 64)
	case float32:
		return strconv.AppendFloat(dst, float64(v), 'g', -1, 32)
	case time.Time:
		return v.AppendFormat(dst, time.RFC3339)
	case time.Duration:
		return append(dst, v.String()...)
	case error:
		return append(dst, v.Error()...)
	case fmt.Stringer:
		return append(dst, v.String()...)
	default:
		return append(dst, fmt.Sprint(v)...)
	}
}

func toString(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case error:
		return v.Error()
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

// goTimeLayout maps a few common .NET-style time formats to Go layouts so that
// Serilog templates like {Time:HH:mm:ss} render intuitively. Unknown formats
// are passed through unchanged (already a valid Go layout, or close enough).
func goTimeLayout(f string) string {
	switch f {
	case "HH:mm:ss":
		return "15:04:05"
	case "HH:mm:ss.fff":
		return "15:04:05.000"
	case "yyyy-MM-dd":
		return "2006-01-02"
	case "yyyy-MM-dd HH:mm:ss":
		return "2006-01-02 15:04:05"
	case "o", "O":
		return time.RFC3339Nano
	default:
		return f
	}
}
