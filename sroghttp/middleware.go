// Package sroghttp provides net/http middleware that attaches a request-scoped
// srog logger to each request: it assigns a request ID, propagates the logger
// through the request context, and logs request completion with structured
// fields. Downstream handlers retrieve the logger with srog.FromContext.
package sroghttp

import (
	"net/http"
	"time"

	"github.com/dvislobokov/srog"
)

// Config controls middleware behavior. Construct it with Option values.
type config struct {
	header   string
	field    string
	genID    func() string
	skip     func(*http.Request) bool
	logStart bool
}

// Option customizes the middleware.
type Option func(*config)

// WithHeader sets the request/response header carrying the request ID
// (default "X-Request-Id"). An incoming value is reused; otherwise one is
// generated and echoed back on the response.
func WithHeader(name string) Option { return func(c *config) { c.header = name } }

// WithField sets the structured field name for the request ID (default
// "RequestId").
func WithField(name string) Option { return func(c *config) { c.field = name } }

// WithIDGenerator overrides how request IDs are generated when none is provided
// by the client (default srog.NewID).
func WithIDGenerator(fn func() string) Option { return func(c *config) { c.genID = fn } }

// WithSkip installs a predicate; requests for which it returns true bypass
// logging entirely (useful for health checks and metrics scrapes).
func WithSkip(fn func(*http.Request) bool) Option { return func(c *config) { c.skip = fn } }

// WithStartLog also logs a line when the request begins, not just on completion.
func WithStartLog(on bool) Option { return func(c *config) { c.logStart = on } }

// Middleware returns net/http middleware that enriches each request with a
// request-scoped logger derived from log and logs completion. The completion
// level is chosen by status code: 5xx -> Error, 4xx -> Warning, else Information.
func Middleware(log *srog.Logger, opts ...Option) func(http.Handler) http.Handler {
	c := config{header: "X-Request-Id", field: "RequestId", genID: srog.NewID}
	for _, o := range opts {
		o(&c)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c.skip != nil && c.skip(r) {
				next.ServeHTTP(w, r)
				return
			}

			id := r.Header.Get(c.header)
			if id == "" {
				id = c.genID()
			}
			w.Header().Set(c.header, id)

			rl := log.ForContext(c.field, id)
			r = r.WithContext(srog.NewContext(r.Context(), rl))

			if c.logStart {
				// Log via Ctx so registered context extractors (e.g. srogotel's
				// trace_id/span_id) enrich the access log just like handler logs.
				srog.Ctx(r.Context()).Information("--> {Method} {Path}", r.Method, r.URL.Path)
			}

			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(sw, r)
			dur := time.Since(start)

			done := srog.Ctx(r.Context()).ForContextValues(map[string]any{
				"status":      sw.status,
				"bytes":       sw.written,
				"remote":      r.RemoteAddr,
				"duration_ms": float64(dur.Microseconds()) / 1000.0,
			})

			const tmpl = "{Method} {Path} -> {Status}"
			switch {
			case sw.status >= 500:
				done.Error(nil, tmpl, r.Method, r.URL.Path, sw.status)
			case sw.status >= 400:
				done.Warning(tmpl, r.Method, r.URL.Path, sw.status)
			default:
				done.Information(tmpl, r.Method, r.URL.Path, sw.status)
			}
		})
	}
}

// statusWriter wraps http.ResponseWriter to record the status code and number
// of bytes written. Unwrap exposes the underlying writer so that
// http.ResponseController (Flush, Hijack, deadlines, ...) keeps working.
type statusWriter struct {
	http.ResponseWriter
	status      int
	written     int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	n, err := w.ResponseWriter.Write(b)
	w.written += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
