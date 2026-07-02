// Package platformlog centralizes one organization's logging convention. Every
// service imports it and wires logging identically; handlers keep using
// srog.FromContext and never reference the field name. Change a constant here
// and all services pick it up on rebuild — no handler edits anywhere.
//
// The same pattern extends to Echo (srogecho.Middleware with WithField/WithHeader)
// and any future transport; only this package changes.
package platformlog

import (
	"context"
	"net/http"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/sroggrpc"
	"github.com/dvislobokov/srog/sroghttp"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// The single source of truth for the correlation-id convention across services.
const (
	Field  = "CorrelationId"    // structured log field name
	Header = "X-Correlation-Id" // HTTP header
	mdKey  = "x-correlation-id" // gRPC metadata key (gRPC lowercases keys)
)

type ctxKey struct{}

// ID returns the correlation id bound to ctx, or "" when none is present. Use it
// only for propagation to downstream calls — for logging, prefer the
// context-scoped logger from srog.FromContext, which already carries the field.
func ID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// HTTPMiddleware assigns or reuses a correlation id, enriches the request-scoped
// srog logger with it (delegating to sroghttp, which also emits the access log),
// and stashes the raw id in the context so handlers can propagate it downstream.
func HTTPMiddleware(log *srog.Logger) func(http.Handler) http.Handler {
	logging := sroghttp.Middleware(log,
		sroghttp.WithField(Field),
		sroghttp.WithHeader(Header),
	)
	return func(next http.Handler) http.Handler {
		// correlate runs first so it can pin the id on the request; the inner
		// sroghttp middleware then reuses that same id for its logging.
		return correlate(logging(next))
	}
}

func correlate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(Header)
		if id == "" {
			id = srog.NewID()
			r.Header.Set(Header, id) // so the inner sroghttp reuses this id
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GRPCUnary is the server interceptor carrying the same convention.
func GRPCUnary(log *srog.Logger) grpc.UnaryServerInterceptor {
	return sroggrpc.UnaryServerInterceptor(log,
		sroggrpc.WithField(Field),
		sroggrpc.WithMetadataKey(mdKey),
	)
}

// PropagateGRPC returns a context carrying the correlation id as outgoing gRPC
// metadata, so the downstream service's interceptor binds the very same id.
func PropagateGRPC(ctx context.Context) context.Context {
	if id := ID(ctx); id != "" {
		return metadata.AppendToOutgoingContext(ctx, mdKey, id)
	}
	return ctx
}

// PropagateHTTP copies the correlation id onto an outgoing HTTP request.
func PropagateHTTP(ctx context.Context, req *http.Request) {
	if id := ID(ctx); id != "" {
		req.Header.Set(Header, id)
	}
}
