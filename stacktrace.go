package srog

import (
	"runtime"
	"strconv"
	"strings"
)

// stackFieldName is the structured field under which a captured call stack is
// stored. It is a single multi-line string so it indexes and renders as one
// readable block in log stores such as Elasticsearch/OpenSearch.
const stackFieldName = "stack"

// StackFieldName is the exported name of the field under which srog stores a
// captured stack trace, and which console sinks pretty-print. Integrations that
// capture their own stack (for example panic recovery, where the useful frames
// only exist at recover time) should attach it under this key.
const StackFieldName = stackFieldName

// maxStackDepth bounds how many frames are captured per error.
const maxStackDepth = 32

// corePkgPath is this package's import path. It is used to recognize (and skip)
// srog's own frames when resolving the caller. Keep it in sync if the module is
// renamed. Subpackages like srogecho have a longer path and are intentionally
// NOT skipped, so their log sites are reported as the caller.
const corePkgPath = "github.com/dvislobokov/srog"

// isSrogCore reports whether fn belongs to this exact package (not a subpackage).
func isSrogCore(fn string) bool {
	return strings.HasPrefix(fn, corePkgPath) &&
		len(fn) > len(corePkgPath) && fn[len(corePkgPath)] == '.'
}

// callerString returns "file:line" for the first frame outside this package, or
// "" if none is found. Walking frames and skipping srog's own (rather than using
// a fixed skip count) keeps it correct regardless of inlining of the level
// methods and of which wrapper called in.
func callerString() string {
	var pcs [16]uintptr
	// Skip runtime.Callers and callerString; begin at callerString's caller.
	n := runtime.Callers(2, pcs[:])
	if n == 0 {
		return ""
	}
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		if f.Function != "" && !isSrogCore(f.Function) {
			return f.File + ":" + strconv.Itoa(f.Line)
		}
		if !more {
			return ""
		}
	}
}

// captureStack formats the current goroutine's stack, beginning at the first
// frame outside this package, as a conventional Go stack-trace string:
//
//	main.startup
//		/app/cmd/main.go:16
//	main.main
//		/app/cmd/main.go:24
//
// Leading srog-core frames (write, the level methods, and the package-level
// *Ctx wrappers) are stripped so the trace starts at the code that logged —
// regardless of how many wrapper frames or inlining sit in between. It returns
// "" when no frames are available.
func captureStack() string {
	var pcs [maxStackDepth]uintptr
	// Skip runtime.Callers and captureStack; begin at captureStack's caller.
	n := runtime.Callers(2, pcs[:])
	if n == 0 {
		return ""
	}
	frames := runtime.CallersFrames(pcs[:n])
	var b strings.Builder
	skipping := true
	for {
		f, more := frames.Next()
		if f.Function == "" {
			break
		}
		// Stop at the runtime entrypoints; they add noise.
		if f.Function == "runtime.main" || f.Function == "runtime.goexit" {
			break
		}
		// Drop srog's own leading plumbing frames so the trace starts at the
		// caller; once a non-core frame is seen, keep everything above it.
		if skipping {
			if isSrogCore(f.Function) {
				if !more {
					break
				}
				continue
			}
			skipping = false
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(f.Function)
		b.WriteString("\n\t")
		b.WriteString(f.File)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(f.Line))
		if !more {
			break
		}
	}
	return b.String()
}
