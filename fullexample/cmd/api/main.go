// Command api is the composition root of the orders service. It is the only
// place that knows every concrete type: it constructs observability, builds the
// dependency graph (domain <- app <- adapters), assembles the HTTP middleware
// chain, and runs the server with graceful shutdown.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/fullexample/internal/adapter/httpapi"
	"github.com/dvislobokov/srog/fullexample/internal/adapter/memrepo"
	"github.com/dvislobokov/srog/fullexample/internal/app"
	"github.com/dvislobokov/srog/fullexample/internal/platform"
	"github.com/dvislobokov/srog/srogotel"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
)

func main() {
	// --- observability ---
	log := platform.NewLogger()
	defer log.Close()
	srog.SetDefault(log)

	tp, err := platform.NewTracerProvider(os.Stderr, "orders-api")
	if err != nil {
		log.Fatal(err, "init tracing")
	}
	otel.SetTracerProvider(tp)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tp.Shutdown(ctx)
	}()
	srogotel.Install() // trace_id/span_id -> every context-scoped log

	// --- dependency graph: domain <- app <- adapters ---
	repo := memrepo.New()
	svc := app.NewOrderService(repo, otel.Tracer("orders"))
	handler := httpapi.NewHandler(svc)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders", handler.CreateOrder)
	mux.HandleFunc("GET /orders/{id}", handler.GetOrder)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	// Middleware chain, innermost first: routes -> request-scoped logging ->
	// OpenTelemetry server span. otelhttp is outermost so the span exists in the
	// context before anything logs, letting srogotel attach trace_id/span_id.
	var root http.Handler = mux
	root = platform.RequestLogging(log)(root)
	root = otelhttp.NewHandler(root, "orders-api")

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           root,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// --- run with graceful shutdown ---
	go func() {
		log.Information("orders-api listening on {Addr}", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error(err, "server error")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Information("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error(err, "graceful shutdown failed")
	}
}
