<div align="center">

# srog

**Structured logging for Go with [Serilog](https://serilog.net/)-style message templates — powered by [zerolog](https://github.com/rs/zerolog).**

Write the message once. Get a human-readable line **and** typed structured fields — for free.

[![CI](https://github.com/dvislobokov/srog/actions/workflows/ci.yml/badge.svg)](https://github.com/dvislobokov/srog/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/dvislobokov/srog.svg)](https://pkg.go.dev/github.com/dvislobokov/srog)
[![Go Report Card](https://goreportcard.com/badge/github.com/dvislobokov/srog)](https://goreportcard.com/report/github.com/dvislobokov/srog)
[![Go](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Built on zerolog](https://img.shields.io/badge/built%20on-zerolog-5C6BC0)](https://github.com/rs/zerolog)
[![Templates](https://img.shields.io/badge/templates-Serilog--style-FF6F00)](https://messagetemplates.org/)
[![Hot path](https://img.shields.io/badge/hot%20path-0%20allocs-2ea44f)](#-performance)
[![License](https://img.shields.io/badge/license-MIT-blue)](#-license)

</div>

```go
log := srog.MustNew(srog.WithConsole())
log.Information("User {Username} logged in from {IP}", "neo", "10.0.0.1")
```

<table>
<tr><td><b>Console</b> — clean, for humans</td><td><b>JSON</b> — structured, for machines</td></tr>
<tr>
<td>

```text
13:04:05 INF User neo logged in from 10.0.0.1
```

</td>
<td>

```json
{"level":"info","Username":"neo","IP":"10.0.0.1",
 "@mt":"User {Username} logged in from {IP}",
 "message":"User neo logged in from 10.0.0.1"}
```

</td>
</tr>
</table>

The named holes `{Username}` and `{IP}` become **typed structured fields**, and `@mt` preserves the
raw template so log pipelines can group events by their template identity — exactly like Serilog.

---

## ✨ Features

- 🧩 **Serilog message templates** — `{Name}`, destructuring `{@obj}`, stringify `{$obj}`, alignment, formats, positional holes.
- ⚡ **Fast** — template parsing is cached; field binding uses a type switch, not reflection. **0 allocations** on the structured hot path.
- 🪵 **Multi-sink fan-out** — pretty console *and* rotated JSON files at once, each with its own format and level.
- ♻️ **Log rotation** — by size, by time (hourly/daily), with age/backup retention and gzip.
- 📦 **Ships anywhere** — NDJSON output drops straight into **Fluent Bit / Elasticsearch / OpenSearch / Loki**.
- 🧵 **Request-scoped logging** — carry a `RequestId` & service name through `context.Context`.
- 🌐 **Batteries included** — `net/http` middleware and gRPC interceptors.
- 🔎 **Readable stack traces** — captured on `Error`/`Fatal`, pretty in console, single indexable string in JSON.

## 📑 Table of contents

- [Install](#-install)
- [Quick start](#-quick-start)
- [Log levels](#-log-levels)
- [Message templates](#-message-templates)
- [Enrichment](#-enrichment)
- [Sinks & output](#-sinks--output)
- [Rotation](#-rotation)
- [Console vs JSON](#-console-vs-json)
- [Fluent Bit / ELK](#-fluent-bit--elk)
- [Request-scoped logging](#-request-scoped-logging)
  - [HTTP middleware](#http-middleware-srogsroghttp)
  - [gRPC interceptors](#grpc-interceptors-srogsroggrpc)
- [Global logger](#-global-logger)
- [Performance](#-performance)
- [License](#-license)

## 📥 Install

```bash
go get github.com/dvislobokov/srog
```

> Requires Go 1.23+ (the `srog/srogotel` module requires Go 1.25+, dictated by
> its OpenTelemetry dependencies). The core package and `srog/sroghttp` depend
> only on [zerolog](https://github.com/rs/zerolog) and
> [lumberjack](https://github.com/natefinch/lumberjack); `srog/sroggrpc` adds
> `google.golang.org/grpc`.

## 🚀 Quick start

```go
package main

import "github.com/dvislobokov/srog"

func main() {
    log := srog.NewConsole() // colorized console at Debug, stack traces on

    log.Information("listening on {Host}:{Port}", "0.0.0.0", 8080)
    log.Warning("cache miss rate {Rate:0.0}%", 12.5)

    if err := connect(); err != nil {
        log.Error(err, "failed to reach {Service}", "postgres")
    }
}
```

## 🎚 Log levels

Serilog names map onto zerolog levels:

| srog              | zerolog | notes                             |
|-------------------|---------|-----------------------------------|
| `Verbose`         | trace   |                                   |
| `Debug`           | debug   |                                   |
| `Information`     | info    | `Info` is a shorthand alias       |
| `Warning`         | warn    |                                   |
| `Error(err, …)`   | error   | `err` attached as `error` field   |
| `Fatal(err, …)`   | fatal   | calls `os.Exit(1)`                |

## 🧩 Message templates

| Form            | Meaning                                                       |
|-----------------|---------------------------------------------------------------|
| `{Name}`        | Scalar property — bound as a typed field (string/int/float/…) |
| `{@Name}`       | **Destructure** — serialize the value as a structured object  |
| `{$Name}`       | **Stringify** — force the value to its string form            |
| `{Name:format}` | Format specifier (`{T:HH:mm:ss}`, `{N:x}`, …)                 |
| `{Name,10}`     | Right-align to width 10; negative width left-aligns           |
| `{0}` `{1}`     | Positional holes bound by argument index                      |
| `{{` / `}}`     | Literal `{` / `}`                                             |

Surplus arguments beyond the holes are attached as `extra_N` fields, so data is
never silently dropped. Missing arguments leave the hole text in the message.

## 🏷 Enrichment

```go
reqLog := log.ForContext("RequestId", "req-7")
reqLog.Information("handling {Path}", "/checkout")   // every event carries RequestId

multi := log.ForContextValues(map[string]any{"service": "api", "version": 3})
svc   := log.Named("billing")                        // sugar for service=billing
```

## 🪵 Sinks & output

A logger fans out to any number of **sinks**, each with its own format and
minimum level. The classic layout — pretty console for humans, rotated JSON
files for shipping — is one constructor call:

```go
log, err := srog.New(
    srog.WithLevel(srog.DebugLevel),       // default level for sinks
    srog.WithStackTrace(true),             // stacks on Error/Fatal

    // colorized, human-readable console (parameters omitted)
    srog.WithConsole(srog.MinLevel(srog.DebugLevel)),

    // machine-readable JSON to a rotating file
    srog.WithFile("/var/log/app/app.log",
        srog.MinLevel(srog.InformationLevel),
        srog.Rotate(srog.Rotation{
            MaxSizeMB:  100,          // rotate past 100 MB
            MaxBackups: 10,           // keep 10 old files
            MaxAgeDays: 30,           // delete files older than 30 days
            Compress:   true,         // gzip rotated files
            Every:      srog.Daily,   // also rotate at midnight (or srog.Hourly)
        }),
    ),
)
if err != nil {
    panic(err)
}
defer log.Close()                          // flush & close file sinks
```

| Constructor           | Default format | Destination     |
|-----------------------|----------------|-----------------|
| `WithConsole(...)`    | console        | `os.Stdout`     |
| `WithFile(path, ...)` | JSON           | file at `path`  |
| `WithWriter(w, ...)`  | JSON           | any `io.Writer` |

**Per-sink options:** `MinLevel(l)` · `AsJSON()` · `AsConsole()` · `AsECS()` · `AsOTel()` · `AsTemplate("...")` · `NoColor()` · `Rotate(Rotation{...})` · `Async(n)`

**Output templates.** `AsTemplate` renders a sink through a Serilog-style output
template — compose the line from placeholders, each supporting the same
`,alignment` and `:format` specifiers as message templates:

```go
srog.WithConsole(srog.AsTemplate(
    "[{Timestamp:15:04:05} {Level:u3}] {Message}{NewLine}{Exception}"))
// 	[13:04:05 WRN] cache miss on user:42

srog.WithFile("app.log", srog.AsTemplate(
    "{Timestamp:rfc3339} level={Level:w} msg=\"{Message}\" {Properties}")) // logfmt-ish
```

| Placeholder | Renders |
|---|---|
| `{Timestamp[:layout]}` | Event time — Go layout, friendly name (`rfc3339`, `datetime`, …), or `.NET`-style (`HH:mm:ss`); bare prints the field as written |
| `{Level[:u3\|w3\|u\|w]}` | `u3`/`w3` → `INF`/`inf`; `u`/`w` → `INFORMATION`/`information`; bare → `info` |
| `{Message}` / `{MessageTemplate}` | Rendered message / raw `@mt` |
| `{Exception}` | Error text, stack trace on the next line; empty when no error |
| `{Caller}` | `file:line` (with `WithCaller(true)`) |
| `{NewLine}` | `\n` |
| `{Properties[:j]}` | Every field not otherwise consumed, as `k=v` pairs (`:j` → one JSON object) |
| `{AnyField}` | Any event field by name — `{RequestId}`, `{Amount,10:.2f}`, … |

Alignment pads (`{Level,-5:u3}` → `ERR  `), `{{`/`}}` escape braces, and a field
absent from the event renders as empty. Fields not referenced by the template are
omitted unless `{Properties}` is present — like a console sink, it is a
presentation layer.

With no sink option, the logger defaults to JSON on stdout. `New` returns an
error if a file cannot be opened; `MustNew` panics instead; `NewConsole()` is a
zero-config colorized console for local dev.

<details>
<summary><b>Logger-wide options</b></summary>

```go
srog.WithLevel(srog.DebugLevel)
srog.WithRenderedMessage(false)       // structured-only: 0 allocations/event
srog.WithCaller(true)                 // reports the real call site, not srog internals
srog.WithTimestamp(true)
srog.WithStackTrace(true)
srog.WithTimeFormat(srog.TimeRFC3339Nano) // or srog.TimeUnix for epoch

// Reliability & throughput
srog.WithErrorHandler(func(err error) { /* count / alert / fall back */ })
srog.WithSampling(srog.BurstLimit(100, time.Second, srog.EveryN(100))) // flood control
// ...and per file sink: srog.WithFile(path, srog.Async(4096)) to move I/O off the request path
```

</details>

<details>
<summary><b>Context-scoped logging & trace correlation</b></summary>

```go
// Middleware stores a request-scoped logger; handlers pull it back out:
srog.Ctx(ctx).Information("processing {OrderId}", id)
srog.InfoCtx(ctx, "charged {Amount}", 999) // package-level shorthand

// Register once so every Ctx/*Ctx log carries fields from the context
// (the srogotel module ships an OpenTelemetry trace_id/span_id extractor):
srog.AddContextField(func(ctx context.Context) []srog.Field { ... })
```

</details>

## 🗂 Configuration from a file

Everything above can also be declared in a `srog.Config` and loaded from JSON
(or YAML — the struct carries `yaml` tags, so `gopkg.in/yaml.v3` decodes it
without srog itself depending on a YAML parser):

```json
{
  "level": "information",
  "caller": true,
  "stackTrace": true,
  "timeFormat": "rfc3339nano",
  "sinks": [
    { "type": "console", "target": "stderr", "level": "debug" },
    { "type": "file", "path": "/var/log/app.log", "level": "warning",
      "rotation": { "maxSizeMB": 100, "maxBackups": 10, "compress": true, "every": "daily" } }
  ]
}
```

```go
log, err := srog.NewFromConfigFile("logging.json")
// or, to compose with programmatic options:
cfg, _ := srog.LoadConfigFile("logging.json")
opts, _ := cfg.Options()
log, _ := srog.New(append(opts, srog.WithWriter(buf))...)
```

`level`/`format`/`every`/`timeFormat` accept the same friendly names shown
throughout this README (case-insensitive); an unknown `timeFormat` is treated as
a raw Go layout. Invalid values fail fast with an error from `Build`.

### Configuration reference

**Top-level fields** (each maps 1:1 to a `With*` option; omit a field to keep its
default):

| Field | Type | Values | Default | Meaning |
|-------|------|--------|---------|---------|
| `level` | string | `verbose`/`trace`, `debug`, `information`/`info`, `warning`/`warn`, `error`, `fatal` | `information` | Logger-wide minimum level for sinks that don't set their own. |
| `render` | bool | `true` / `false` | `true` | Render the human-readable `message` field. Turn off for max throughput when you only consume structured fields; console sinks need it on. |
| `caller` | bool | `true` / `false` | `false` | Annotate each event with the calling `file:line`. |
| `timestamp` | bool | `true` / `false` | `true` | Add a timestamp to each event. |
| `stackTrace` | bool | `true` / `false` | `false` | Capture a call stack whenever an error is logged via `Error`/`Fatal`. |
| `timeFormat` | string | `rfc3339`, `rfc3339nano`, `datetime`, `dateonly`, `timeonly`, `kitchen`, `unix`, `unixms`, `unixmicro`, `unixnano`, or a raw Go layout | `rfc3339` | Timestamp layout. The `unix*` names emit epoch numbers; the rest emit strings. |
| `sinks` | array | see below | one JSON sink on stdout | Output destinations; each event fans out to every sink that admits its level. |

**Sink fields** (each entry in `sinks`):

| Field | Type | Values | Default | Meaning |
|-------|------|--------|---------|---------|
| `type` | string | `console`, `file`, `stdout`, `stderr`, or any name registered via `srog.RegisterSinkType` (e.g. `otlp` once `srog/srogotel` is imported) | — (**required**) | Destination kind. `stdout`/`stderr` write to the standard streams; `console` is a stream sink that defaults to the colorized console format. |
| `target` | string | `stdout`, `stderr` | `stdout` | Which stream a `console` sink writes to. Ignored for other types. |
| `path` | string | any file path | — (**required** for `file`) | File to write. The parent directory must already exist. |
| `level` | string | same names as top-level `level` | inherits logger `level` | Per-sink minimum level, so one sink can show `debug` while another keeps only `warning`+. |
| `format` | string | `json`, `console`/`text`, `ecs`, `otel`/`opentelemetry`/`otlp`, `template` | `console` for `type: console`, otherwise `json` | Serialization. `ecs` = Elastic Common Schema field names; `otel` = OpenTelemetry OTLP/JSON log records; `template` = Serilog-style output template (requires `template`). |
| `template` | string | output template | — | Serilog-style output template, e.g. `"[{Timestamp:15:04:05} {Level:u3}] {Message}{NewLine}{Exception}"`. Setting it implies `format: template`. |
| `noColor` | bool | `true` / `false` | `false` | Disable ANSI colors (applies to the `console` format only). |
| `rotation` | object | see below | none | Size/time/age rotation. `file` sinks only. |
| `options` | object | type-specific | none | Settings for a sink type registered via `RegisterSinkType`; built-in types ignore it. See the `otlp` options under [OpenTelemetry logs](#opentelemetry-logs--otlpjson-out-of-the-box). |

**Rotation fields** (`rotation` object on a `file` sink):

| Field | Type | Values | Default | Meaning |
|-------|------|--------|---------|---------|
| `maxSizeMB` | int | ≥ 0 | `0` (no size trigger) | Roll over once the file exceeds this many megabytes. |
| `maxBackups` | int | ≥ 0 | `0` (keep all) | Maximum number of rotated files to retain. |
| `maxAgeDays` | int | ≥ 0 | `0` (no age limit) | Delete rotated files older than this many days. |
| `compress` | bool | `true` / `false` | `false` | Gzip rotated files. |
| `localTime` | bool | `true` / `false` | `false` | Timestamp backup names in local time instead of UTC. |
| `every` | string | `none`/`""`, `hourly`, `daily` | `none` | Time-based rotation cadence, combined with the size trigger (first to fire wins). |

See [`examples/formats`](examples/formats) for one `logging.json` that exercises
every sink type and format at once.

## ♻️ Rotation

`Rotate(srog.Rotation{...})` on a file sink combines size, time, and age
triggers (size/age/backup/compress via
[lumberjack](https://github.com/natefinch/lumberjack); `Every` adds hourly or
daily rotation). A file rolls over when **either** the size or time trigger
fires.

- **Size only** → set just `MaxSizeMB`.
- **Time only** → set just `Every` (`srog.Hourly` / `srog.Daily`).
- **Both** → set both; the first to fire wins.

## 🎨 Console vs JSON

A console sink is purely a *presentation* layer for local development. It prints
one clean line — timestamp, level, rendered message (and error text) — and
**omits every structured parameter**. The values still appear inside the
rendered message; the separate `Username=…`, `@mt=…` fields do not.

```text
2026-06-15T13:04:25+03:00 ERR startup failed at config connection refused
    main.startup
        /app/cmd/main.go:16
    main.main
        /app/cmd/main.go:24
```

The same event in JSON keeps the full structured payload:

```json
{"level":"error","error":"connection refused",
 "stack":"main.startup\n\t/app/cmd/main.go:16\nmain.main\n\t/app/cmd/main.go:24",
 "@mt":"startup failed at {Stage}","Stage":"config","message":"startup failed at config"}
```

`WithStackTrace(true)` captures the call stack at the log site on `Error`/`Fatal`.
In JSON it is a single multi-line `stack` string — one field that indexes and
renders cleanly in Elasticsearch/OpenSearch — and in console it is pretty-printed
beneath the message.

## 📦 Fluent Bit / ELK

JSON sinks emit newline-delimited JSON (NDJSON) with stable `time`, `level`, and
`message` keys, so Fluent Bit ingests them with a plain `tail` input:

```ini
[INPUT]
    Name        tail
    Path        /var/log/app/*.log
    Parser      json

[PARSER]
    Name        json
    Format      json
    Time_Key    time
    Time_Format %Y-%m-%dT%H:%M:%S%z   # RFC3339 (srog default)
```

For epoch timestamps, set `srog.WithTimeFormat(srog.TimeUnixMs)` and let
Fluent Bit handle the numeric `time` field. Console sinks are for humans and are
not meant to be shipped.

### ECS (Elastic Common Schema) — ELK out of the box

For a zero-mapping path into Elasticsearch/Kibana, use the ECS sink format. It
renames fields to the schema Kibana expects (`@timestamp`, `log.level`,
`error.message`, `error.stack_trace`, `log.origin.file.*`) and injects
`ecs.version`, so events index into a standard ES index with no Logstash rules:

```go
srog.WithFile("/var/log/app.log", srog.AsECS())   // or "format": "ecs" in Config
```

```json
{"@timestamp":"2026-06-30T21:00:00Z","log.level":"error","message":"failed save",
 "error.message":"boom","message_template.text":"failed {Op}","Op":"save","ecs.version":"8.11.0"}
```

The recommended shipping path stays **logger → NDJSON/ECS file → Filebeat/Fluent
Bit → ES**; core srog does not open network connections itself.

### OpenTelemetry logs — OTLP/JSON out of the box

For an OpenTelemetry logs pipeline, use the OTel sink format. Each event is
written as a single OTLP/JSON log record (one `LogRecord` per line), mapped onto
the [OpenTelemetry Logs Data Model](https://opentelemetry.io/docs/specs/otel/logs/data-model/):
`time` → `timeUnixNano`, `level` → `severityNumber`/`severityText`, `message` →
`body`, `trace_id`/`span_id` → `traceId`/`spanId`, and every remaining field
becomes a typed `attributes` entry (`error` → `exception.message`, `stack` →
`exception.stacktrace`, `caller` → `code.filepath`/`code.lineno`). This is the
form the Collector's `otlpjson` receiver and file exporter read, so events feed
straight into any OTel logs backend (Loki, Elastic, …):

```go
srog.WithFile("/var/log/app.log", srog.AsOTel())   // or "format": "otel" in Config
```

```json
{"timeUnixNano":"1751317200000000000","severityNumber":17,"severityText":"ERROR",
 "body":{"stringValue":"failed save"},"attributes":[{"key":"Op","value":{"stringValue":"save"}},
 {"key":"exception.message","value":{"stringValue":"boom"}}]}
```

Pair it with **`srog/srogotel`** (`srogotel.Install()`) so context-scoped logs
carry the active span's `trace_id`/`span_id` — the OTel writer then promotes them
into `traceId`/`spanId`, joining logs to traces in your backend.

To skip the file/shipper hop entirely, the **`srog/srogotel`** module also ships
logs straight to the Collector over OTLP via the OTel Logs Bridge API. With a
zero config it reuses the process-global `LoggerProvider` — the one you already
configured next to your tracer/meter providers — so logs inherit the same
exporter, resource (`service.name`, …), and batching:

```go
// Reuse the already-configured global LoggerProvider (traces/metrics setup):
opt, sink, err := srogotel.WithLogs(ctx, srogotel.Config{})

// ...or a specific provider: srogotel.Config{Provider: loggerProvider}

// ...or build a private OTLP exporter with explicit parameters:
opt, sink, err = srogotel.WithLogs(ctx, srogotel.Config{
    Endpoint: "collector:4317",        // Protocol: "http" for OTLP/HTTP (4318)
    Insecure: true,
    Headers:  map[string]string{"authorization": "Bearer ..."},
})

if err != nil { /* ... */ }
defer sink.Close() // flushes an owned provider on shutdown
log := srog.MustNew(srog.WithConsole(), opt)
```

Each event is mapped onto the Logs Data Model exactly like the OTel writer above,
and `trace_id`/`span_id` (from `srogotel.Install()`) become the record's trace
context, so logs land in the Collector already joined to their traces. Batching,
retries, and delivery are handled by the OTel SDK's `BatchProcessor`, off the
logging hot path. See `examples/otel-logs` for a runnable end-to-end setup.

The same sink is available from the declarative JSON config: importing
`srog/srogotel` (a blank import works) registers the `otlp` sink type. An empty
`options` object reuses the global `LoggerProvider`; setting `endpoint` builds a
private exporter:

```go
import _ "github.com/dvislobokov/srog/srogotel" // registers "type": "otlp"
```

```json
{"sinks": [
  {"type": "otlp"},
  {"type": "otlp", "level": "warning", "options": {
      "endpoint": "collector:4317", "protocol": "grpc", "insecure": true,
      "headers": {"authorization": "Bearer ..."}, "timeout": "10s",
      "scopeName": "my-service",
      "attributes": {"data_stream.dataset": "billing"}}}
]}
```

The optional `attributes` map (also `Config.Attributes` in code) is stamped onto
every record — use it for routing hints the Collector reads, such as
`data_stream.dataset` or `elasticsearch.index` for the elasticsearch exporter's
dynamic index. An event field with the same name wins over the static value.

The sink parses srog's JSON events, so leave the entry's `format` at its default.
Third-party sinks can plug into the config the same way — register a factory with
`srog.RegisterSinkType(name, factory)` and read type-specific settings from the
entry's `options` via `SinkSpec.DecodeOptions`; a writer that implements
`io.Closer` is closed by `Logger.Close`.

If you cannot run a shipper, the opt-in **`srog/srogelastic`** module writes
directly to Elasticsearch's `_bulk` API — fully asynchronous, so it never blocks
the application (Write only enqueues; a background worker batches, retries with
backoff, and drops on a full queue). It depends only on the standard library:

```go
opt, sink, err := srogelastic.WithElasticsearch(srogelastic.Config{
    Addresses: []string{"http://localhost:9200"},
    Index:     "app-logs-%{2006.01.02}", // %{…} = Go time layout, resolved per batch (UTC)
    Gzip:      true,                     // compress bulk bodies
    OnError:   func(err error) { /* metrics / alert */ },
})
if err != nil { /* ... */ }
defer sink.Close() // flushes the queue on shutdown
log := srog.MustNew(srog.WithConsole(), opt) // opt ships events as ECS
```

Delivery is resilient by default: network errors, `429` and `5xx` responses are
retried with exponential backoff, and when a `_bulk` response reports partial
failures only the rejected documents are resent — an accepted document is never
duplicated and one poisoned document cannot sink a batch. Set
`DataStream: true` to target a data stream (bulk actions switch to `create`).

The module also registers itself with the declarative config, so importing it
(blank import works) enables:

```json
{"sinks": [{
    "type": "elasticsearch",
    "options": {
        "addresses": ["http://es:9200"],
        "index": "app-logs-%{2006.01.02}",
        "gzip": true,
        "dataStream": false,
        "username": "elastic", "password": "secret",
        "batchSize": 500, "flushInterval": "5s", "timeout": "30s"
    }
}]}
```

Events default to ECS formatting; set the entry's `format` to override.
`Logger.Close` flushes and closes the sink.

## 🧵 Request-scoped logging

Enrich a logger once and stash it in the `context.Context`. Downstream code —
including services that know nothing about HTTP or gRPC — pulls it back out with
`srog.FromContext`, which **never returns nil** (it falls back to the default
logger).

```go
// at the edge: derive a request-scoped logger and put it in the context
rl := log.ForContext("RequestId", srog.NewID())
ctx = srog.NewContext(ctx, rl)        // or: rl.IntoContext(ctx)

// deep inside a service: retrieve it; tag it with the service name
func (s *Billing) Charge(ctx context.Context, cents int) {
    log := srog.FromContext(ctx).Named("billing") // adds service=billing
    log.Information("charging {Amount} cents", cents)
}
```

Every line then shares the same `RequestId`, so a single query pulls the whole request:

```json
{"level":"info","RequestId":"f93e…401f","message":"handling checkout"}
{"level":"info","RequestId":"f93e…401f","service":"billing","Amount":999,"message":"charging 999 cents"}
{"level":"info","RequestId":"f93e…401f","status":200,"duration_ms":1.2,"Method":"GET","Path":"/checkout","message":"GET /checkout -> 200"}
```

> **Integration modules.** The core library depends only on zerolog and
> lumberjack. Framework integrations live in **separate modules** so their
> dependencies never reach core: `srog/sroghttp` (stdlib, in-tree),
> `srog/sroggrpc` (gRPC), `srog/srogecho` (Echo), `srog/srogotel`
> (OpenTelemetry trace correlation + OTLP log export), and `srog/srogelastic`
> (direct Elasticsearch `_bulk` sink, stdlib-only). Import only what you use.

### HTTP middleware (`srog/sroghttp`)

Standard-library `net/http` middleware — no extra dependencies. It reuses an
incoming `X-Request-Id` or generates one, echoes it on the response, injects the
request-scoped logger into the context, and logs completion with method, path,
status, byte count, remote address, and duration. The level is chosen by status:
**5xx → Error, 4xx → Warning, else Information.**

```go
mw := sroghttp.Middleware(log,
    sroghttp.WithSkip(func(r *http.Request) bool { return r.URL.Path == "/healthz" }),
    sroghttp.WithStartLog(true), // also log when the request begins
)
http.ListenAndServe(":8080", mw(router))

// in a handler:
func handler(w http.ResponseWriter, r *http.Request) {
    srog.FromContext(r.Context()).Information("handling checkout")
}
```

Options: `WithHeader` · `WithField` · `WithIDGenerator` · `WithSkip` · `WithStartLog`

### gRPC interceptors (`srog/sroggrpc`)

Server interceptors that mirror the HTTP behavior using gRPC metadata
(`x-request-id`), logging completion at a level chosen from the gRPC status code:

```go
srv := grpc.NewServer(
    grpc.UnaryInterceptor(sroggrpc.UnaryServerInterceptor(log)),
    grpc.StreamInterceptor(sroggrpc.StreamServerInterceptor(log)),
)
// handlers use srog.FromContext(ctx) exactly as in HTTP
```

> **Note:** `srog/sroggrpc` pulls in `google.golang.org/grpc`. The core logger
> and `srog/sroghttp` have no gRPC dependency — split this subpackage into its
> own module if you want to keep the core dependency-light.

## 🌍 Global logger

A package-level default mirrors Serilog's static `Log` facade:

```go
srog.SetDefault(srog.NewConsole())
srog.Information("started on {Port}", 8080)
srog.Error(err, "boom in {Op}", "flush")
```

## ⚡ Performance

Template parsing is cached (string literals hit the cache ~100% of the time), and
value→field binding uses a type switch instead of reflection for common types.
Measured on a Ryzen 5 8400F:

| Benchmark               | ns/op | B/op | allocs/op |
|-------------------------|------:|-----:|----------:|
| `Rendered`              |  ~196 |   48 |         1 |
| `StructuredOnly`        |  ~125 |    0 |     **0** |
| `ParseCached`           |   ~12 |    0 |         0 |

The single allocation on the rendered path is the message string handed to
zerolog. Disable rendering with `WithRenderedMessage(false)` for a fully
zero-allocation hot path when you only consume structured fields plus `@mt`.

```bash
go test ./...                       # run the suite
go test -bench . -benchmem ./...    # run benchmarks
```

## 📄 License

MIT — see [`LICENSE`](LICENSE).

<div align="center"><sub>Built with ❤️ on top of <a href="https://github.com/rs/zerolog">zerolog</a>.</sub></div>
