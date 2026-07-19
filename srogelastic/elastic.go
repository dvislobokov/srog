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
//
// Importing the package also registers the "elasticsearch" sink type with
// srog's declarative config, so a JSON/YAML config can declare the sink
// without code (see the package-level factory in config.go):
//
//	{"sinks": [{"type": "elasticsearch",
//	            "options": {"addresses": ["http://es:9200"], "index": "app-logs"}}]}
package srogelastic

import (
	"bytes"
	"compress/gzip"
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
	// Index is the destination index or data stream. It may embed Go time
	// layouts as %{layout} placeholders, resolved in UTC when each batch is
	// shipped — e.g. "app-logs-%{2006.01.02}" writes to a daily index.
	Index string

	// DataStream targets a data stream instead of a classic index: bulk actions
	// use "create" (data streams reject "index") and Index must name the data
	// stream.
	DataStream bool

	// Gzip compresses each bulk request body (Content-Encoding: gzip). Bulk
	// NDJSON compresses well, which matters when Elasticsearch is across a WAN.
	Gzip bool

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
	// exponential backoff (default 3). Retries cover network errors, 5xx and
	// 429 (Too Many Requests) responses, and — per document — items the bulk
	// response reports as failed with a retryable status; only those documents
	// are resent, so one poisoned document cannot sink a whole batch. Other
	// 4xx responses are not retried.
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
	action string // bulk action line: {"index":{}} or {"create":{}}

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

	action := `{"index":{}}`
	if cfg.DataStream {
		action = `{"create":{}}`
	}

	s := &Sink{
		cfg:    cfg,
		client: client,
		action: action,
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
	failed, err := s.deliver(batch)
	if failed > 0 {
		s.failed.Add(uint64(failed))
	}
	if err != nil && s.cfg.OnError != nil {
		s.cfg.OnError(err)
	}
}

// deliver ships docs, retrying with backoff up to MaxRetries. After each
// attempt only the documents that actually failed retryably are resent, so a
// partially accepted batch is never duplicated. It returns how many documents
// were permanently lost and the error from the last failing attempt.
func (s *Sink) deliver(docs [][]byte) (failed int, err error) {
	remaining := docs
	var lastErr error
	for attempt := 0; attempt <= s.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(backoff(attempt))
		}
		retry, permFailed, postErr := s.post(remaining)
		failed += permFailed
		if len(retry) == 0 {
			// Done: either everything was accepted (postErr nil) or the rest
			// failed permanently (postErr says why).
			return failed, postErr
		}
		remaining = retry
		lastErr = postErr
	}
	return failed + len(remaining), lastErr
}

// post performs a single bulk request for docs. It returns the documents that
// should be retried (whole batch on network errors, 429 and 5xx responses;
// individual documents whose bulk item status is retryable), the count of
// documents rejected permanently, and the error describing this attempt's
// failure (nil on full success).
func (s *Sink) post(docs [][]byte) (retry [][]byte, permFailed int, err error) {
	body, err := s.encodeBulk(docs)
	if err != nil {
		return nil, len(docs), err
	}
	addr := s.nextAddr()
	index := resolveIndex(s.cfg.Index, time.Now().UTC())
	url := strings.TrimRight(addr, "/") + "/" + index + "/_bulk"

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, len(docs), fmt.Errorf("srogelastic: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if s.cfg.Gzip {
		req.Header.Set("Content-Encoding", "gzip")
	}
	s.auth(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return docs, 0, fmt.Errorf("srogelastic: post bulk: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		// Overload or server fault: the whole batch is worth retrying.
		return docs, 0, fmt.Errorf("srogelastic: bulk HTTP %d: %s", resp.StatusCode, snippet(data))
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		// Other 4xx (bad request, auth): retrying cannot help.
		return nil, len(docs), fmt.Errorf("srogelastic: bulk HTTP %d: %s", resp.StatusCode, snippet(data))
	}

	// A 2xx can still contain per-item failures, flagged by "errors":true.
	var r bulkResponse
	if json.Unmarshal(data, &r) != nil || !r.Errors {
		return nil, 0, nil
	}
	if len(r.Items) != len(docs) {
		// The response does not line up with what was sent; without a mapping
		// there is nothing safe to resend. Report, do not retry.
		return nil, 0, fmt.Errorf("srogelastic: bulk reported item errors: %s", snippet(data))
	}

	var firstErr string
	for i, item := range r.Items {
		st := item.status()
		switch {
		case st >= 200 && st < 300:
		case st == http.StatusTooManyRequests || st >= 500:
			retry = append(retry, docs[i])
		default:
			permFailed++
		}
		if firstErr == "" && (st < 200 || st >= 300) {
			firstErr = item.errorText()
		}
	}
	if len(retry) > 0 || permFailed > 0 {
		err = fmt.Errorf("srogelastic: bulk items failed (%d retryable, %d permanent): %s",
			len(retry), permFailed, firstErr)
	}
	return retry, permFailed, err
}

// bulkResponse is the subset of the _bulk response needed for per-item retries.
// Each item is keyed by its action ("index" or "create").
type bulkResponse struct {
	Errors bool       `json:"errors"`
	Items  []bulkItem `json:"items"`
}

type bulkItem map[string]struct {
	Status int             `json:"status"`
	Error  json.RawMessage `json:"error"`
}

func (it bulkItem) status() int {
	for _, v := range it {
		return v.Status
	}
	return 0
}

func (it bulkItem) errorText() string {
	for _, v := range it {
		return snippet(v.Error)
	}
	return ""
}

// encodeBulk assembles the NDJSON _bulk payload — an action line before each
// document — gzip-compressing it when configured. Documents are expected to be
// single JSON objects (one per event).
func (s *Sink) encodeBulk(docs [][]byte) ([]byte, error) {
	var b bytes.Buffer
	var w io.Writer = &b
	var gz *gzip.Writer
	if s.cfg.Gzip {
		gz = gzip.NewWriter(&b)
		w = gz
	}
	for _, doc := range docs {
		io.WriteString(w, s.action)
		io.WriteString(w, "\n")
		w.Write(bytes.TrimRight(doc, "\r\n"))
		io.WriteString(w, "\n")
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			return nil, fmt.Errorf("srogelastic: gzip bulk body: %w", err)
		}
	}
	return b.Bytes(), nil
}

// resolveIndex expands %{layout} placeholders in an index pattern using now,
// e.g. "app-logs-%{2006.01.02}" -> "app-logs-2026.07.19". A pattern without
// placeholders is returned unchanged; an unclosed %{ is kept literally.
func resolveIndex(pattern string, now time.Time) string {
	i := strings.Index(pattern, "%{")
	if i < 0 {
		return pattern
	}
	var b strings.Builder
	for i >= 0 {
		j := strings.Index(pattern[i:], "}")
		if j < 0 {
			break
		}
		b.WriteString(pattern[:i])
		b.WriteString(now.Format(pattern[i+2 : i+j]))
		pattern = pattern[i+j+1:]
		i = strings.Index(pattern, "%{")
	}
	b.WriteString(pattern)
	return b.String()
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
