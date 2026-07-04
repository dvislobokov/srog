package platform

import (
	"net/http"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/sroghttp"
)

// The service's correlation convention lives in one place.
const (
	correlationField  = "CorrelationId"
	correlationHeader = "X-Correlation-Id"
)

// RequestLogging assigns/reuses a correlation id, injects a request-scoped srog
// logger into the request context, and logs completion — all via sroghttp.
// Handlers and use cases retrieve the logger with srog.Ctx / srog.FromContext.
func RequestLogging(log *srog.Logger) func(http.Handler) http.Handler {
	return sroghttp.Middleware(log,
		sroghttp.WithField(correlationField),
		sroghttp.WithHeader(correlationHeader),
	)
}
