<div align="center">

# srog

**Structured logging for Go with [Serilog](https://serilog.net/)-style message templates — powered by [zerolog](https://github.com/rs/zerolog).**

Write the message once. Get a human-readable line **and** typed structured fields — for free.

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

> Requires Go 1.23+. The core package and `srog/sroghttp` depend only on
> [zerolog](https://github.com/rs/zerolog) and
> [lumberjack](https://github.com/natefinch/lumberjack); `srog/sroggrpc` adds
> `google.golang.org/grpc`.

## 🚀 Quick start

```go
package main

import "srog"

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

**Per-sink options:** `MinLevel(l)` · `AsJSON()` · `AsConsole()` · `NoColor()` · `Rotate(Rotation{...})`

With no sink option, the logger defaults to JSON on stdout. `New` returns an
error if a file cannot be opened; `MustNew` panics instead; `NewConsole()` is a
zero-config colorized console for local dev.

<details>
<summary><b>Logger-wide options</b></summary>

```go
srog.WithLevel(srog.DebugLevel)
srog.WithRenderedMessage(false)       // structured-only: 0 allocations/event
srog.WithCaller(true)
srog.WithTimestamp(true)
srog.WithStackTrace(true)
srog.WithTimeFormat(time.RFC3339Nano) // or zerolog.TimeFormatUnix for epoch
```

</details>

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

For epoch timestamps, set `srog.WithTimeFormat(zerolog.TimeFormatUnixMs)` and let
Fluent Bit handle the numeric `time` field. Console sinks are for humans and are
not meant to be shipped.

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
