package srog

import "sync"

// capture describes how a property value should be bound to the log event.
type capture uint8

const (
	// captureDefault binds the value using a typed zerolog field, falling back
	// to scalar string rendering for unknown types.
	captureDefault capture = iota
	// captureDestructure ("@") serializes the value as a structured object.
	captureDestructure
	// captureStringify ("$") forces the value to its string representation.
	captureStringify
)

// holeKind distinguishes named holes from positional ones ({0}, {1}, ...).
type holeKind uint8

const (
	holeNamed holeKind = iota
	holePositional
)

// token is one piece of a parsed message template: either literal text or a
// property hole to be filled by a positional argument.
type token struct {
	// text is the literal segment for text tokens.
	text string

	// isHole reports whether this token is a property placeholder.
	isHole bool

	kind     holeKind
	capture  capture
	name     string // property name for named holes
	pos      int    // index for positional holes
	align    int    // ,alignment specifier (0 = none)
	format   string // :format specifier
	rawIndex int    // ordinal of this hole among all holes, used for arg binding
}

// template is a parsed, immutable message template ready for fast rendering.
type template struct {
	raw       string
	tokens    []token
	holeCount int
	// named is true if every hole is named; allows skipping work when false.
	hasHoles bool
}

// templateCache memoizes parsed templates keyed by their raw string. Templates
// are typically string literals, so the cache hit rate approaches 100%.
var templateCache sync.Map // map[string]*template

// parse returns a cached parsed template for raw, parsing it on first use.
func parse(raw string) *template {
	if v, ok := templateCache.Load(raw); ok {
		return v.(*template)
	}
	t := parseTemplate(raw)
	actual, _ := templateCache.LoadOrStore(raw, t)
	return actual.(*template)
}

// parseTemplate scans raw into tokens. It tolerates malformed holes by emitting
// them as literal text rather than failing, matching Serilog's resilience.
func parseTemplate(raw string) *template {
	t := &template{raw: raw}
	var sb []byte // accumulates literal text between holes
	holeIdx := 0

	flush := func() {
		if len(sb) > 0 {
			t.tokens = append(t.tokens, token{text: string(sb)})
			sb = sb[:0]
		}
	}

	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch c {
		case '{':
			// Escaped "{{" -> literal "{".
			if i+1 < len(raw) && raw[i+1] == '{' {
				sb = append(sb, '{')
				i++
				continue
			}
			end := indexByte(raw, '}', i+1)
			if end < 0 {
				// Unterminated hole: treat the rest as literal text.
				sb = append(sb, raw[i:]...)
				i = len(raw)
				continue
			}
			inner := raw[i+1 : end]
			h, ok := parseHole(inner)
			if !ok {
				// Not a valid hole; keep braces as literal text.
				sb = append(sb, raw[i:end+1]...)
				i = end
				continue
			}
			flush()
			h.rawIndex = holeIdx
			holeIdx++
			t.tokens = append(t.tokens, h)
			i = end
		case '}':
			// Escaped "}}" -> literal "}".
			if i+1 < len(raw) && raw[i+1] == '}' {
				sb = append(sb, '}')
				i++
				continue
			}
			sb = append(sb, c)
		default:
			sb = append(sb, c)
		}
	}
	flush()

	t.holeCount = holeIdx
	t.hasHoles = holeIdx > 0
	return t
}

// parseHole parses the inside of a "{...}" placeholder. The grammar is:
//
//	[@|$] (name | index) [ ,alignment ] [ :format ]
//
// It returns ok=false for empty or clearly invalid content so the caller can
// fall back to treating the braces as literal text.
func parseHole(inner string) (token, bool) {
	if inner == "" {
		return token{}, false
	}

	h := token{isHole: true}

	// Capturing operator.
	switch inner[0] {
	case '@':
		h.capture = captureDestructure
		inner = inner[1:]
	case '$':
		h.capture = captureStringify
		inner = inner[1:]
	}
	if inner == "" {
		return token{}, false
	}

	// Split off ":format" (first colon).
	if ci := indexByte(inner, ':', 0); ci >= 0 {
		h.format = inner[ci+1:]
		inner = inner[:ci]
	}

	// Split off ",alignment".
	if ai := indexByte(inner, ',', 0); ai >= 0 {
		align, ok := atoiSigned(inner[ai+1:])
		if !ok {
			return token{}, false
		}
		h.align = align
		inner = inner[:ai]
	}

	if inner == "" {
		return token{}, false
	}

	// Positional ({0}) vs named hole.
	if isAllDigits(inner) {
		pos, ok := atoiSigned(inner)
		if !ok || pos < 0 {
			return token{}, false
		}
		h.kind = holePositional
		h.pos = pos
		return h, true
	}

	if !isValidName(inner) {
		return token{}, false
	}
	h.kind = holeNamed
	h.name = inner
	return h, true
}

// --- small allocation-free helpers ---

func indexByte(s string, b byte, from int) int {
	for i := from; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// atoiSigned parses an optionally-signed base-10 integer without allocating.
func atoiSigned(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	neg := false
	i := 0
	if s[0] == '-' || s[0] == '+' {
		neg = s[0] == '-'
		i = 1
	}
	if i == len(s) {
		return 0, false
	}
	n := 0
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}

// isValidName reports whether s is a valid property name: a letter or underscore
// followed by letters, digits, or underscores.
func isValidName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		isLetter := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		isDigit := c >= '0' && c <= '9'
		if i == 0 {
			if !isLetter {
				return false
			}
		} else if !isLetter && !isDigit {
			return false
		}
	}
	return len(s) > 0
}
