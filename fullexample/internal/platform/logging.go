// Package platform holds infrastructure/composition helpers: constructing the
// logger, the tracer provider, and the HTTP middleware. It is the only place
// that wires srog/OTel specifics; the domain and use cases stay unaware.
package platform

import (
	"os"

	"github.com/dvislobokov/srog"
)

// NewLogger builds the application logger. JSON to stdout keeps the correlated
// fields (CorrelationId, trace_id, span_id) visible; swap for WithConsole in dev
// or add WithFile(..., Async(...)) / AsECS() for shipping.
func NewLogger() *srog.Logger {
	return srog.MustNew(
		srog.WithWriter(os.Stdout, srog.AsJSON()),
		srog.WithLevel(srog.DebugLevel),
		srog.WithStackTrace(true),
		srog.WithTimeFormat(srog.TimeRFC3339),
	)
}
