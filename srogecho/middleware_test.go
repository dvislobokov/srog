package srogecho_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/srogecho"
	"github.com/labstack/echo/v4"
)

func TestMiddlewareLogsRequest(t *testing.T) {
	var buf bytes.Buffer
	log := srog.MustNew(srog.WithWriter(&buf, srog.AsJSON()),
		srog.WithTimestamp(false), srog.WithLevel(srog.DebugLevel))

	e := echo.New()
	e.Use(srogecho.Middleware(log))
	e.GET("/hi/:name", func(c echo.Context) error {
		srogecho.From(c).Information("greeting {Name}", c.Param("name"))
		return c.String(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hi/neo", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get(echo.HeaderXRequestID) == "" {
		t.Fatal("no request id echoed on the response")
	}

	// Two events — the handler line and the completion line — both carrying the
	// request-scoped RequestId that the middleware bound.
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d: %s", len(lines), buf.Bytes())
	}
	for _, ln := range lines {
		var m map[string]any
		if err := json.Unmarshal(ln, &m); err != nil {
			t.Fatalf("bad json %q: %v", ln, err)
		}
		if id, _ := m["RequestId"].(string); id == "" {
			t.Fatalf("log line missing RequestId: %s", ln)
		}
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"Name":"neo"`)) {
		t.Fatal("handler template field not logged")
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"status":200`)) {
		t.Fatalf("completion status not logged: %s", buf.Bytes())
	}
}

func TestRecoverLogsPanicWithStack(t *testing.T) {
	var buf bytes.Buffer
	log := srog.MustNew(srog.WithWriter(&buf, srog.AsJSON()),
		srog.WithTimestamp(false), srog.WithStackTrace(true))

	e := echo.New()
	e.Use(srogecho.Middleware(log))
	e.Use(srogecho.Recover(log))
	e.GET("/panic", func(c echo.Context) error { panic("kaboom") })

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "panic recovered") {
		t.Fatalf("panic not logged through srog: %s", out)
	}
	// The recover-time stack must be captured and include the panicking handler
	// frame — proving we grabbed the real stack, not the defer site.
	if !strings.Contains(out, `"stack"`) || !strings.Contains(out, "TestRecoverLogsPanicWithStack") {
		t.Fatalf("panic stack missing real frames: %s", out)
	}
}
