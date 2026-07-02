// Command nethttp shows the canonical pattern: a middleware injects a
// request-scoped logger (carrying a RequestId) into each request's context, and
// any handler pulls it back out — handlers never receive the logger as an
// argument. Uses only core srog and the stdlib sroghttp middleware.
//
//	go run .
//	curl -i localhost:8080/users/7
//	curl -i localhost:8080/users/0     # 404
//	curl -i localhost:8080/boom        # 500 with stack
//	curl -i localhost:8080/health      # not logged (skipped)
package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/sroghttp"
)

func main() {
	log := srog.MustNew(
		srog.WithConsole(srog.MinLevel(srog.DebugLevel)),
		srog.WithFile("./http.logs", srog.MinLevel(srog.InformationLevel), srog.Async(0)),
		srog.WithStackTrace(true),
	)
	defer log.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", getUser)
	mux.HandleFunc("GET /boom", boom)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// The middleware assigns a RequestId, stores an enriched logger in the
	// request context, and logs completion. Health checks are skipped.
	handler := sroghttp.Middleware(log,
		sroghttp.WithStartLog(true),
		sroghttp.WithSkip(func(r *http.Request) bool { return r.URL.Path == "/health" }),
	)(mux)

	log.Information("listening on {Addr}", ":8080")
	if err := http.ListenAndServe(":8080", handler); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err, "server stopped")
	}
}

func getUser(w http.ResponseWriter, r *http.Request) {
	// Pull the injected, request-scoped logger — it already carries RequestId.
	log := srog.FromContext(r.Context())
	id := r.PathValue("id")
	log.Information("looking up user {UserId}", id)

	if id == "0" {
		log.Warning("user {UserId} not found", id)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": "Neo"})
}

func boom(w http.ResponseWriter, r *http.Request) {
	// srog.Ctx pulls the request-scoped logger; the *Ctx package helpers
	// (srog.InfoCtx, srog.ErrorCtx, ...) are shorthands for the same thing.
	srog.Ctx(r.Context()).Error(errors.New("downstream timeout"),
		"failed to build {Report}", "dashboard")
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
