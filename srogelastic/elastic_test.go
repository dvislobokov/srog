package srogelastic_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/srogelastic"
)

func TestBulkDelivery(t *testing.T) {
	var mu sync.Mutex
	var paths, bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.Write([]byte(`{"errors":false,"items":[]}`))
	}))
	defer srv.Close()

	sink, err := srogelastic.New(srogelastic.Config{
		Addresses: []string{srv.URL},
		Index:     "app-logs",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sink.Write([]byte(`{"a":1}`))
	sink.Write([]byte(`{"b":2}`))
	sink.Write([]byte(`{"c":3}`))
	if err := sink.Close(); err != nil { // flushes the queue as one bulk request
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 {
		t.Fatalf("expected 1 bulk request, got %d", len(paths))
	}
	if paths[0] != "/app-logs/_bulk" {
		t.Fatalf("bulk path = %q", paths[0])
	}
	body := bodies[0]
	if n := strings.Count(body, `{"index":{}}`); n != 3 {
		t.Fatalf("expected 3 index actions, got %d in:\n%s", n, body)
	}
	for _, doc := range []string{`{"a":1}`, `{"b":2}`, `{"c":3}`} {
		if !strings.Contains(body, doc) {
			t.Fatalf("bulk body missing %s:\n%s", doc, body)
		}
	}
	if sink.Failed() != 0 || sink.Dropped() != 0 {
		t.Fatalf("unexpected failed=%d dropped=%d", sink.Failed(), sink.Dropped())
	}
}

func TestRetryOn5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable) // fail once
			return
		}
		w.Write([]byte(`{"errors":false}`))
	}))
	defer srv.Close()

	sink, _ := srogelastic.New(srogelastic.Config{
		Addresses:  []string{srv.URL},
		Index:      "logs",
		MaxRetries: 3,
	})
	sink.Write([]byte(`{"x":1}`))
	sink.Close()

	if calls.Load() < 2 {
		t.Fatalf("expected a retry, got %d calls", calls.Load())
	}
	if sink.Failed() != 0 {
		t.Fatalf("delivery should have eventually succeeded, failed=%d", sink.Failed())
	}
}

func TestNonBlockingDrops(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // hold the request so the worker cannot drain the queue
		w.Write([]byte(`{"errors":false}`))
	}))
	defer srv.Close()

	var dropErr error
	sink, _ := srogelastic.New(srogelastic.Config{
		Addresses:     []string{srv.URL},
		Index:         "logs",
		QueueSize:     2,
		BatchSize:     1,
		FlushInterval: time.Hour, // only Close/batch triggers flush
		OnError:       func(err error) { dropErr = err },
	})

	// Flooding past the 2-slot queue while the worker is blocked in the handler
	// must not block the caller — excess events drop.
	start := time.Now()
	for i := 0; i < 500; i++ {
		sink.Write([]byte(`{"x":1}`))
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Write blocked the caller for %v", elapsed)
	}

	close(release) // let the worker finish so Close can complete
	sink.Close()

	if sink.Dropped() == 0 {
		t.Fatal("expected dropped events under a full queue")
	}
	if dropErr == nil || !strings.Contains(dropErr.Error(), "dropped") {
		t.Fatalf("expected drop report via OnError, got %v", dropErr)
	}
}

func TestIntegrationWithSrogECS(t *testing.T) {
	var body string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(b)
		mu.Unlock()
		w.Write([]byte(`{"errors":false}`))
	}))
	defer srv.Close()

	opt, sink, err := srogelastic.WithElasticsearch(srogelastic.Config{
		Addresses: []string{srv.URL},
		Index:     "app",
	})
	if err != nil {
		t.Fatalf("WithElasticsearch: %v", err)
	}
	log := srog.MustNew(opt, srog.WithTimestamp(false))
	log.Info("hello {Who}", "world")
	sink.Close()

	mu.Lock()
	defer mu.Unlock()
	// The sink was wired with AsECS(), so docs must carry ECS field names.
	for _, want := range []string{`"log.level":"info"`, `"message":"hello world"`, `"message_template.text":"hello {Who}"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("shipped doc missing %s:\n%s", want, body)
		}
	}
}
