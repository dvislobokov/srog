// Package srogecho integrates srog with the Echo web framework
// (github.com/labstack/echo/v4). It provides an Echo-native request-logging
// middleware, a panic-recovery middleware that records the panic through srog
// (with the real stack), a From accessor for the request-scoped logger, and an
// EchoLogger adapter so Echo's own internal output flows through srog's sinks.
//
//	e := echo.New()
//	e.Logger = srogecho.EchoLogger(log)          // Echo's internal logs -> srog
//	e.Use(srogecho.Middleware(log))              // request-scoped logger + access log
//	e.Use(srogecho.Recover(log))                 // panics -> srog with stack
//
//	e.GET("/users/:id", func(c echo.Context) error {
//	    srogecho.From(c).Information("fetching {UserId}", c.Param("id"))
//	    ...
//	})
package srogecho

import (
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/dvislobokov/srog"
	"github.com/labstack/echo/v4"
)

// From returns the request-scoped logger stored on the Echo context by
// Middleware, or srog.Default() when the middleware is not installed. It never
// returns nil, so handlers can log unconditionally.
func From(c echo.Context) *srog.Logger {
	return srog.FromContext(c.Request().Context())
}

type config struct {
	header   string
	field    string
	genID    func() string
	skip     func(echo.Context) bool
	logStart bool
}

// Option customizes Middleware.
type Option func(*config)

// WithHeader sets the request/response header carrying the request ID
// (default echo.HeaderXRequestID). An incoming value is reused; otherwise one is
// generated and echoed back on the response.
func WithHeader(name string) Option { return func(c *config) { c.header = name } }

// WithField sets the structured field name for the request ID (default
// "RequestId").
func WithField(name string) Option { return func(c *config) { c.field = name } }

// WithIDGenerator overrides how request IDs are generated when the client does
// not supply one (default srog.NewID).
func WithIDGenerator(fn func() string) Option { return func(c *config) { c.genID = fn } }

// WithSkip installs a predicate; requests for which it returns true bypass
// logging entirely (useful for health checks and metrics scrapes).
func WithSkip(fn func(echo.Context) bool) Option { return func(c *config) { c.skip = fn } }

// WithStartLog also logs a line when the request begins, not just on completion.
func WithStartLog(on bool) Option { return func(c *config) { c.logStart = on } }

// Middleware returns Echo middleware that attaches a request-scoped srog logger
// (enriched with a request ID) to each request and logs completion. The
// completion level is chosen by status code: 5xx -> Error, 4xx -> Warning,
// otherwise Information. Unlike wrapping sroghttp with echo.WrapMiddleware, it
// reads status and byte counts from Echo's own *Response instead of adding a
// second response wrapper.
func Middleware(log *srog.Logger, opts ...Option) echo.MiddlewareFunc {
	c := config{header: echo.HeaderXRequestID, field: "RequestId", genID: srog.NewID}
	for _, o := range opts {
		o(&c)
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ec echo.Context) error {
			if c.skip != nil && c.skip(ec) {
				return next(ec)
			}

			req := ec.Request()
			res := ec.Response()

			id := req.Header.Get(c.header)
			if id == "" {
				id = c.genID()
			}
			res.Header().Set(c.header, id)

			rl := log.ForContext(c.field, id)
			ec.SetRequest(req.WithContext(rl.IntoContext(req.Context())))

			if c.logStart {
				// Via Ctx so context extractors (e.g. srogotel trace_id/span_id)
				// enrich the access log like handler logs do.
				srog.Ctx(ec.Request().Context()).Information("--> {Method} {Path}", req.Method, req.URL.Path)
			}

			start := time.Now()
			err := next(ec)
			// On a returned error Echo has not written the response yet, so
			// res.Status is still 200. Invoke the error handler now to
			// materialize the real status, then swallow the error (return nil)
			// so Echo does not handle it a second time.
			if err != nil {
				ec.Error(err)
			}
			dur := time.Since(start)

			done := srog.Ctx(ec.Request().Context()).ForContextValues(map[string]any{
				"status":      res.Status,
				"bytes":       res.Size,
				"remote":      ec.RealIP(),
				"duration_ms": float64(dur.Microseconds()) / 1000.0,
			})

			const tmpl = "{Method} {Path} -> {Status}"
			switch {
			case res.Status >= 500:
				done.Error(cause(err), tmpl, req.Method, req.URL.Path, res.Status)
			case res.Status >= 400:
				done.Warning(tmpl, req.Method, req.URL.Path, res.Status)
			default:
				done.Information(tmpl, req.Method, req.URL.Path, res.Status)
			}
			return nil
		}
	}
}

// Recover returns Echo middleware that recovers panics and logs them through
// srog with the panic's real stack trace (captured at recover time, where the
// frames still exist), then responds 500 via Echo's error handler. Install it
// inside Middleware (i.e. e.Use(Middleware(...)) before e.Use(Recover(...))) so
// the request-scoped logger and the completion line are still produced.
func Recover(log *srog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			defer func() {
				r := recover()
				if r == nil {
					return
				}
				if r == http.ErrAbortHandler {
					panic(r) // not a real panic; let the server handle it
				}
				err, ok := r.(error)
				if !ok {
					err = fmt.Errorf("%v", r)
				}
				// Attach the recover-time stack ourselves under srog's stack
				// field, and disable srog's own capture so the field is not
				// duplicated with a less useful defer-site trace.
				From(c).
					WithStackTrace(false).
					ForContext(srog.StackFieldName, string(debug.Stack())).
					Error(err, "panic recovered: {Panic}", err.Error())
				c.Error(echo.NewHTTPError(http.StatusInternalServerError))
			}()
			return next(c)
		}
	}
}

// cause unwraps an echo.HTTPError to the more meaningful internal error, so the
// logged "error" field reflects the real failure rather than the HTTP wrapper.
func cause(err error) error {
	var he *echo.HTTPError
	if errors.As(err, &he) && he.Internal != nil {
		return he.Internal
	}
	return err
}
