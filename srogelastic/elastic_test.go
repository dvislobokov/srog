package srogelastic_test

import (
	"compress/gzip"
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

func TestRetryOn429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests) // overloaded once
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
		t.Fatalf("429 must be retried, got %d calls", calls.Load())
	}
	if sink.Failed() != 0 {
		t.Fatalf("delivery should have eventually succeeded, failed=%d", sink.Failed())
	}
}

func TestPerItemRetryResendsOnlyFailedDocs(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		if calls.Add(1) == 1 {
			// First doc accepted, second rejected retryably (429).
			w.Write([]byte(`{"errors":true,"items":[` +
				`{"index":{"status":201}},` +
				`{"index":{"status":429,"error":{"reason":"es_rejected_execution_exception"}}}]}`))
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
	sink.Write([]byte(`{"a":1}`))
	sink.Write([]byte(`{"b":2}`))
	sink.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("expected 2 bulk requests, got %d", len(bodies))
	}
	// The retry must carry only the rejected document, not the accepted one.
	if strings.Contains(bodies[1], `{"a":1}`) {
		t.Fatalf("retry re-sent an already accepted doc:\n%s", bodies[1])
	}
	if !strings.Contains(bodies[1], `{"b":2}`) {
		t.Fatalf("retry missing the rejected doc:\n%s", bodies[1])
	}
	if sink.Failed() != 0 {
		t.Fatalf("all docs eventually delivered, failed=%d", sink.Failed())
	}
}

func TestPermanentItemFailureNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"errors":true,"items":[` +
			`{"index":{"status":400,"error":{"reason":"mapper_parsing_exception"}}}]}`))
	}))
	defer srv.Close()

	var gotErr error
	sink, _ := srogelastic.New(srogelastic.Config{
		Addresses:  []string{srv.URL},
		Index:      "logs",
		MaxRetries: 3,
		OnError:    func(err error) { gotErr = err },
	})
	sink.Write([]byte(`{"bad":true}`))
	sink.Close()

	if calls.Load() != 1 {
		t.Fatalf("a 400 item must not be retried, got %d calls", calls.Load())
	}
	if sink.Failed() != 1 {
		t.Fatalf("expected 1 permanently failed doc, got %d", sink.Failed())
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "permanent") {
		t.Fatalf("expected item-failure report via OnError, got %v", gotErr)
	}
}

func TestDataStreamUsesCreateAction(t *testing.T) {
	var mu sync.Mutex
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(b)
		mu.Unlock()
		w.Write([]byte(`{"errors":false}`))
	}))
	defer srv.Close()

	sink, _ := srogelastic.New(srogelastic.Config{
		Addresses:  []string{srv.URL},
		Index:      "logs-app-default",
		DataStream: true,
	})
	sink.Write([]byte(`{"x":1}`))
	sink.Close()

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(body, `{"create":{}}`) || strings.Contains(body, `{"index":{}}`) {
		t.Fatalf("data stream bulk must use create actions:\n%s", body)
	}
}

func TestDatedIndexPattern(t *testing.T) {
	var mu sync.Mutex
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		path = r.URL.Path
		mu.Unlock()
		w.Write([]byte(`{"errors":false}`))
	}))
	defer srv.Close()

	before := time.Now().UTC().Format("2006.01.02")
	sink, _ := srogelastic.New(srogelastic.Config{
		Addresses: []string{srv.URL},
		Index:     "app-logs-%{2006.01.02}",
	})
	sink.Write([]byte(`{"x":1}`))
	sink.Close()
	after := time.Now().UTC().Format("2006.01.02")

	mu.Lock()
	defer mu.Unlock()
	if path != "/app-logs-"+before+"/_bulk" && path != "/app-logs-"+after+"/_bulk" {
		t.Fatalf("dated index not resolved, path = %q", path)
	}
}

func TestGzipBody(t *testing.T) {
	var mu sync.Mutex
	var encoding, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("body is not gzip: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		b, _ := io.ReadAll(zr)
		mu.Lock()
		encoding = r.Header.Get("Content-Encoding")
		body = string(b)
		mu.Unlock()
		w.Write([]byte(`{"errors":false}`))
	}))
	defer srv.Close()

	sink, _ := srogelastic.New(srogelastic.Config{
		Addresses: []string{srv.URL},
		Index:     "logs",
		Gzip:      true,
	})
	sink.Write([]byte(`{"x":1}`))
	sink.Close()

	mu.Lock()
	defer mu.Unlock()
	if encoding != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", encoding)
	}
	if !strings.Contains(body, `{"index":{}}`) || !strings.Contains(body, `{"x":1}`) {
		t.Fatalf("decompressed bulk body wrong:\n%s", body)
	}
	if sink.Failed() != 0 {
		t.Fatalf("failed=%d", sink.Failed())
	}
}

func TestConfigSinkType(t *testing.T) {
	var mu sync.Mutex
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(b)
		mu.Unlock()
		w.Write([]byte(`{"errors":false}`))
	}))
	defer srv.Close()

	cfg, err := srog.LoadConfig(strings.NewReader(`{
		"sinks": [{
			"type": "elasticsearch",
			"options": {
				"addresses": ["` + srv.URL + `"],
				"index": "app",
				"flushInterval": "1h",
				"batchSize": 100
			}
		}]
	}`))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	log, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	log.Info("hello {Who}", "config")
	log.Close() // must flush and close the registered sink

	mu.Lock()
	defer mu.Unlock()
	// The factory defaults to ECS formatting.
	for _, want := range []string{`"log.level":"info"`, `"message":"hello config"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("shipped doc missing %s:\n%s", want, body)
		}
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
