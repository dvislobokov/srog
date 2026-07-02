// Package sroggrpc provides gRPC server interceptors that attach a
// request-scoped srog logger to each call: they extract or generate a request
// ID from metadata, propagate the logger through the call context, and log call
// completion with the gRPC status code and duration. Handlers retrieve the
// logger with srog.FromContext.
package sroggrpc

import (
	"context"
	"time"

	"github.com/dvislobokov/srog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type config struct {
	metaKey string
	field   string
	genID   func() string
}

// Option customizes the interceptors.
type Option func(*config)

// WithMetadataKey sets the gRPC metadata key carrying the request ID
// (default "x-request-id"). gRPC lowercases metadata keys.
func WithMetadataKey(key string) Option { return func(c *config) { c.metaKey = key } }

// WithField sets the structured field name for the request ID (default
// "RequestId").
func WithField(name string) Option { return func(c *config) { c.field = name } }

// WithIDGenerator overrides request-ID generation when the client supplied none
// (default srog.NewID).
func WithIDGenerator(fn func() string) Option { return func(c *config) { c.genID = fn } }

func newConfig(opts []Option) config {
	c := config{metaKey: "x-request-id", field: "RequestId", genID: srog.NewID}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// UnaryServerInterceptor returns a grpc.UnaryServerInterceptor that injects a
// request-scoped logger and logs each call's outcome.
func UnaryServerInterceptor(log *srog.Logger, opts ...Option) grpc.UnaryServerInterceptor {
	c := newConfig(opts)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, rl, id := c.enrich(ctx, log)
		_ = grpc.SetHeader(ctx, metadata.Pairs(c.metaKey, id))

		start := time.Now()
		resp, err := handler(ctx, req)
		c.logDone(rl, info.FullMethod, err, time.Since(start))
		return resp, err
	}
}

// StreamServerInterceptor returns a grpc.StreamServerInterceptor that injects a
// request-scoped logger and logs each stream's outcome.
func StreamServerInterceptor(log *srog.Logger, opts ...Option) grpc.StreamServerInterceptor {
	c := newConfig(opts)
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, rl, id := c.enrich(ss.Context(), log)
		_ = grpc.SetHeader(ctx, metadata.Pairs(c.metaKey, id))

		start := time.Now()
		err := handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
		c.logDone(rl, info.FullMethod, err, time.Since(start))
		return err
	}
}

// enrich resolves the request ID, derives the request-scoped logger, and stores
// it in the returned context.
func (c config) enrich(ctx context.Context, log *srog.Logger) (context.Context, *srog.Logger, string) {
	id := requestIDFromMD(ctx, c.metaKey)
	if id == "" {
		id = c.genID()
	}
	rl := log.ForContext(c.field, id)
	return srog.NewContext(ctx, rl), rl, id
}

// logDone emits the completion event at a level chosen from the gRPC status code.
func (c config) logDone(rl *srog.Logger, method string, err error, dur time.Duration) {
	code := status.Code(err)
	done := rl.ForContextValues(map[string]any{
		"method":      method,
		"code":        code.String(),
		"duration_ms": float64(dur.Microseconds()) / 1000.0,
	})
	const tmpl = "gRPC {Method} -> {Code}"
	switch code {
	case codes.OK:
		done.Information(tmpl, method, code.String())
	case codes.Canceled, codes.InvalidArgument, codes.NotFound, codes.AlreadyExists,
		codes.PermissionDenied, codes.Unauthenticated, codes.FailedPrecondition,
		codes.OutOfRange, codes.ResourceExhausted:
		done.Warning(tmpl, method, code.String())
	default:
		done.Error(err, tmpl, method, code.String())
	}
}

// requestIDFromMD returns the first value for key in the incoming metadata, or
// "" if absent.
func requestIDFromMD(ctx context.Context, key string) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get(key); len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}

// wrappedStream overrides Context so that the request-scoped logger reaches
// streaming handlers via ss.Context().
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }
