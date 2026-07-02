// Package srogelastic provides an opt-in srog sink that ships log events directly
// to Elasticsearch via its _bulk API. It is designed to never affect the
// application: Write only copies the event into a bounded in-memory queue and
// returns immediately — all batching, HTTP, retries, and backoff happen on a
// background goroutine. When the queue is full events are dropped (and counted)
// rather than blocking the caller.
//
// It talks to Elasticsearch over plain HTTP (the _bulk endpoint is NDJSON), so
// it pulls in no Elasticsearch client dependency — only the standard library.
//
//	sink, err := srogelastic.New(srogelastic.Config{
//	    Addresses: []string{"http://localhost:9200"},
//	    Index:     "app-logs",
//	    OnError:   func(err error) { metrics.LogShipErrors.Inc() },
//	})
//	if err != nil { log.Fatal(err, "elastic sink") }
//	defer sink.Close() // flushes the queue
//
//	logger := srog.MustNew(
//	    srog.WithConsole(),
//	    srog.WithWriter(sink, srog.AsECS()), // ECS field names for Kibana
//	)
package srogelastic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dvislobokov/srog"
)

// Defaults applied by New when a Config field is left zero.
const (
	defaultBatchSize     = 500
	defaultFlushInterval = 5 * time.Second
	defaultQueueSize     = 10000
	defaultMaxRetries    = 3
	defaultTimeout       = 30 * time.Second
	maxBackoff           = 5 * time.Second
)

// Config configures a Sink. Only Addresses and Index are required.
type Config struct {
	// Addresses are Elasticsearch node base URLs (e.g. "http://es:9200").
	// Multiple addresses are tried round-robin for basic high availability.
	Addresses []string
	// Index is the destination index or data stream.
	Index string

	// Username and Password enable HTTP basic auth (optional).
	Username string
	Password string
	// APIKey enables ApiKey auth (optional; takes precedence over basic auth).
	APIKey string

	// BatchSize flushes once this many events are queued (default 500).
	BatchSize int
	// FlushInterval flushes at least this often even when BatchSize is not
	// reached (default 5s).
	FlushInterval time.Duration
	// QueueSize bounds the in-memory queue. When full, new events are dropped
	// and counted (default 10000).
	QueueSize int
	// MaxRetries is the number of retries per failed bulk request, with
	// exponential backoff (default 3). 4xx responses are not retried.
	MaxRetries int
	// Timeout bounds each HTTP request (default 30s). Ignored if Client is set.
	Timeout time.Duration

	// OnError, if set, receives delivery failures and a drop summary on Close.
	// It must be safe for concurrent use and must not log through this sink.
	OnError func(error)
	// Client optionally overrides the HTTP client (for custom TLS, proxies, ...).
	Client *http.Client
}

// Sink is an io.WriteCloser that ships events to Elasticsearch from a background
// worker. Construct it with New and pass it to srog.WithWriter (typically with
// srog.AsECS()). Always Close it so the queue is flushed on shutdown.
type Sink struct {
	cfg    Config
	client *http.Client

	queue chan []byte
	stop  chan struct{}
	done  chan struct{}

	closeOnce sync.Once
	dropped   atomic.Uint64
	failed    atomic.Uint64
	addrIdx   atomic.Uint32
}

// New validates cfg, applies defaults, and starts the background worker.
func New(cfg Config) (*Sink, error) {
	if len(cfg.Addresses) == 0 {
		return nil, errors.New("srogelastic: at least one address is required")
	}
	if strings.TrimSpace(cfg.Index) == "" {
		return nil, errors.New("srogelastic: index is required")
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultBatchSize
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = defaultFlushInterval
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}

	s := &Sink{
		cfg:    cfg,
		client: client,
		queue:  make(chan []byte, cfg.QueueSize),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go s.run()
	return s, nil
}

// Write enqueues a copy of p and returns immediately. It never blocks: if the
// queue is full the event is dropped and counted. The returned n is always
// len(p) so it composes cleanly with srog's writer chain.
func (s *Sink) Write(p []byte) (int, error) {
	// zerolog reuses p after Write returns, so the bytes must be copied.
	buf := make([]byte, len(p))
	copy(buf, p)
	select {
	case s.queue <- buf:
	default:
		s.dropped.Add(1)
	}
	return len(p), nil
}

// Close stops the worker after flushing everything already queued, reports the
// total dropped through OnError, and is safe to call once.
func (s *Sink) Close() error {
	s.closeOnce.Do(func() { close(s.stop) })
	<-s.done
	if d := s.dropped.Load(); d > 0 && s.cfg.OnError != nil {
		s.cfg.OnError(fmt.Errorf("srogelastic: dropped %d events (queue full)", d))
	}
	return nil
}

// Dropped returns the number of events dropped because the queue was full.
func (s *Sink) Dropped() uint64 { return s.dropped.Load() }

// Failed returns the number of events that could not be delivered after retries.
func (s *Sink) Failed() uint64 { return s.failed.Load() }

func (s *Sink) run() {
	defer close(s.done)
	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()

	batch := make([][]byte, 0, s.cfg.BatchSize)
	for {
		select {
		case <-s.stop:
			s.drainAndFlush(batch)
			return
		case doc := <-s.queue:
			batch = append(batch, doc)
			if len(batch) >= s.cfg.BatchSize {
				s.flush(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				s.flush(batch)
				batch = batch[:0]
			}
		}
	}
}

// drainAndFlush ships everything still buffered in the queue, then a final batch.
func (s *Sink) drainAndFlush(batch [][]byte) {
	for {
		select {
		case doc := <-s.queue:
			batch = append(batch, doc)
			if len(batch) >= s.cfg.BatchSize {
				s.flush(batch)
				batch = batch[:0]
			}
		default:
			s.flush(batch)
			return
		}
	}
}

func (s *Sink) flush(batch [][]byte) {
	if len(batch) == 0 {
		return
	}
	body := buildBulk(batch)
	if err := s.send(body); err != nil {
		s.failed.Add(uint64(len(batch)))
		if s.cfg.OnError != nil {
			s.cfg.OnError(err)
		}
	}
}

// buildBulk assembles the NDJSON _bulk payload: an index action line before each
// document. Documents are expected to be single JSON objects (one per event).
func buildBulk(batch [][]byte) []byte {
	var b bytes.Buffer
	for _, doc := range batch {
		b.WriteString(`{"index":{}}`)
		b.WriteByte('\n')
		b.Write(bytes.TrimRight(doc, "\r\n"))
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func (s *Sink) send(body []byte) error {
	var lastErr error
	for attempt := 0; attempt <= s.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(backoff(attempt))
		}
		addr := s.nextAddr()
		url := strings.TrimRight(addr, "/") + "/" + s.cfg.Index + "/_bulk"

		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("srogelastic: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-ndjson")
		s.auth(req)

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("srogelastic: post bulk: %w", err)
			continue // network error: retry
		}
		err = checkResponse(resp)
		if err == nil {
			return nil
		}
		lastErr = err
		// Client errors (bad request, auth) will not succeed on retry.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return err
		}
	}
	return lastErr
}

func checkResponse(resp *http.Response) error {
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("srogelastic: bulk HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	// A 200 can still contain per-item failures, flagged by "errors":true.
	var r struct {
		Errors bool `json:"errors"`
	}
	if json.Unmarshal(data, &r) == nil && r.Errors {
		return fmt.Errorf("srogelastic: bulk reported item errors: %s", snippet(data))
	}
	return nil
}

func (s *Sink) auth(req *http.Request) {
	switch {
	case s.cfg.APIKey != "":
		req.Header.Set("Authorization", "ApiKey "+s.cfg.APIKey)
	case s.cfg.Username != "":
		req.SetBasicAuth(s.cfg.Username, s.cfg.Password)
	}
}

func (s *Sink) nextAddr() string {
	if len(s.cfg.Addresses) == 1 {
		return s.cfg.Addresses[0]
	}
	i := s.addrIdx.Add(1)
	return s.cfg.Addresses[int(i)%len(s.cfg.Addresses)]
}

func backoff(attempt int) time.Duration {
	d := 100 * time.Millisecond << (attempt - 1) // 100ms, 200ms, 400ms, ...
	if d > maxBackoff || d <= 0 {
		return maxBackoff
	}
	return d
}

func snippet(b []byte) string {
	const max = 200
	if len(b) > max {
		return string(b[:max]) + "..."
	}
	return string(b)
}

// WithElasticsearch is a convenience that builds a Sink and returns a srog.Option
// wiring it as an ECS-formatted writer sink, along with the Sink so the caller
// can Close it on shutdown:
//
//	opt, sink, err := srogelastic.WithElasticsearch(cfg)
//	if err != nil { ... }
//	defer sink.Close()
//	log := srog.MustNew(srog.WithConsole(), opt)
func WithElasticsearch(cfg Config) (srog.Option, *Sink, error) {
	s, err := New(cfg)
	if err != nil {
		return nil, nil, err
	}
	return srog.WithWriter(s, srog.AsECS()), s, nil
}
