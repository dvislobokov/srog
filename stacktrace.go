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

// maxStackDepth bounds how many frames are captured per error.
const maxStackDepth = 32

// captureStack records the call stack starting skip frames above runtime.Callers
// (skip=1 is captureStack's own caller) and formats it as a conventional Go
// stack-trace string:
//
//	main.startup
//		/app/cmd/main.go:16
//	main.main
//		/app/cmd/main.go:24
//
// It returns "" when no frames are available.
func captureStack(skip int) string {
	var pcs [maxStackDepth]uintptr
	n := runtime.Callers(skip+1, pcs[:])
	if n == 0 {
		return ""
	}
	frames := runtime.CallersFrames(pcs[:n])
	var b strings.Builder
	for {
		f, more := frames.Next()
		if f.Function == "" {
			break
		}
		// Stop at the runtime entrypoints; they add noise.
		if f.Function == "runtime.main" || f.Function == "runtime.goexit" {
			break
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
