package sroghttp_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"srog"
	"srog/sroghttp"
)

func newLogger() (*srog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	l := srog.MustNew(srog.WithWriter(&buf, srog.AsJSON()), srog.WithTimestamp(false), srog.WithLevel(srog.DebugLevel))
	return l, &buf
}

// lastWith returns the last JSON log line that contains the given key.
func lastWith(t *testing.T, buf *bytes.Buffer, key string) map[string]any {
	t.Helper()
	var found map[string]any
	sc := bufio.NewScanner(strings.NewReader(buf.String()))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid json line %q: %v", line, err)
		}
		if _, ok := m[key]; ok {
			found = m
		}
	}
	if found == nil {
		t.Fatalf("no log line with key %q in:\n%s", key, buf.String())
	}
	return found
}

func TestMiddlewareLogsCompletion(t *testing.T) {
	log, buf := newLogger()

	var handlerSawID string
	h := sroghttp.Middleware(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The handler can log through the request-scoped logger.
		srog.FromContext(r.Context()).Debug("in handler")
		handlerSawID = w.Header().Get("X-Request-Id")
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest(http.MethodGet, "/widgets/7", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	id := rec.Header().Get("X-Request-Id")
	if id == "" {
		t.Fatal("middleware did not set X-Request-Id response header")
	}
	if handlerSawID != id {
		t.Fatalf("handler saw id %q, response had %q", handlerSawID, id)
	}

	m := lastWith(t, buf, "status")
	if m["level"] != "warn" { // 404 -> Warning
		t.Errorf("want warn level for 404, got %v", m["level"])
	}
	if m["status"].(float64) != 404 || m["Method"] != "GET" || m["Path"] != "/widgets/7" {
		t.Errorf("completion fields wrong: %v", m)
	}
	if m["RequestId"] != id {
		t.Errorf("RequestId field %v != header %q", m["RequestId"], id)
	}
}

func TestMiddlewareReusesIncomingID(t *testing.T) {
	log, buf := newLogger()
	h := sroghttp.Middleware(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "client-supplied")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-Id") != "client-supplied" {
		t.Fatalf("incoming request id not reused: %q", rec.Header().Get("X-Request-Id"))
	}
	m := lastWith(t, buf, "status")
	if m["RequestId"] != "client-supplied" || m["level"] != "info" {
		t.Errorf("unexpected completion event: %v", m)
	}
}

func TestMiddlewareSkip(t *testing.T) {
	log, buf := newLogger()
	mw := sroghttp.Middleware(log, sroghttp.WithSkip(func(r *http.Request) bool {
		return r.URL.Path == "/healthz"
	}))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if strings.TrimSpace(buf.String()) != "" {
		t.Fatalf("skipped request should produce no logs, got:\n%s", buf.String())
	}
}
