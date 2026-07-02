// Command elk ships logs directly to Elasticsearch via the opt-in srogelastic
// sink while also printing to the console. The sink is fully asynchronous, so
// logging never blocks the application even if Elasticsearch is slow or down.
//
// Requires a reachable Elasticsearch (e.g. `docker run -p 9200:9200 \
// -e discovery.type=single-node elasticsearch:8.13.0`). Then:
//
//	go run .
//	# query it:  curl localhost:9200/app-logs/_search?pretty
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/srogelastic"
)

func main() {
	opt, sink, err := srogelastic.WithElasticsearch(srogelastic.Config{
		Addresses:     []string{"http://localhost:9200"},
		Index:         "app-logs",
		BatchSize:     200,
		FlushInterval: 2 * time.Second,
		// Delivery problems surface here instead of affecting the app.
		OnError: func(err error) { fmt.Fprintln(os.Stderr, "elastic:", err) },
	})
	if err != nil {
		panic(err)
	}
	defer sink.Close() // flush the queue on shutdown

	log := srog.MustNew(
		srog.WithConsole(), // humans read the console
		opt,                // machines get ECS docs in Elasticsearch
	)
	defer log.Close()

	log.Information("shipping to elasticsearch index {Index}", "app-logs")
	for i := 0; i < 5; i++ {
		log.Information("processed batch {Batch} with {Count} items", i, i*100)
	}
	log.Error(errors.New("payment declined"), "charge failed for {UserId}", "u-99")

	// In a real service the process keeps running; here we pause so the async
	// worker's flush interval elapses before the deferred Close.
	time.Sleep(3 * time.Second)
}
