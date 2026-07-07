// Command formats builds one logger that fans out to every output variant srog
// supports, driven entirely by the declarative logging.json next to this file:
//
//   - console → stdout   colorized, human-friendly (debug and up)
//   - console → stderr   errors only, no color (systemd/journald friendly)
//   - stdout  → json     raw NDJSON stream (machine-readable)
//   - stderr  → console   warnings and up, no color
//   - file    → json     plain NDJSON on disk
//   - file    → ecs      Elastic Common Schema, rotating daily (Kibana-ready)
//   - file    → otel     OpenTelemetry OTLP/JSON, rotating hourly (OTel Collector)
//
// Every sink can carry its own minimum level, so the same event renders in as
// many places as its level allows. Run it and inspect ./logs/*.log:
//
//	go run .
package main

import (
	"errors"
	"os"

	"github.com/dvislobokov/srog"
)

func main() {
	// File sinks require their parent directory to exist.
	if err := os.MkdirAll("logs", 0o755); err != nil {
		panic(err)
	}

	log, err := srog.NewFromConfigFile("logging.json")
	if err != nil {
		panic(err)
	}
	defer log.Close() // flushes files and any async queues on shutdown

	log.Debug("only the stdout console sink shows this (level=debug)")
	log.Information("processing order {OrderId} for {User}", 42, "neo")
	log.Warning("low stock for {Sku}: {Left} left", "A-100", 3)
	log.Error(errors.New("gateway timeout"), "charge failed for order {OrderId}", 42)

	// Same line, every format: NDJSON in app.json.log, ECS field names in
	// app.ecs.log, and an OTLP/JSON LogRecord in app.otel.log.
	log.Information("shipped order {OrderId} via {Carrier}", 42, "DHL")
}
