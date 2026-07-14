# Changelog

All notable changes to this project are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Submodules are versioned with prefixed tags (e.g. `srogotel/v1.1.0`).

## [1.1.1] / srogelastic/v1.1.1 / sroggrpc/v1.1.1 / srogecho/v1.1.1 — 2026-07-14

### Changed

- **Minimum Go version lowered from 1.25 to 1.23** for the core module and the
  `srogelastic`, `sroggrpc`, and `srogecho` submodules. To reach that floor,
  minimum dependency versions were lowered accordingly (`golang.org/x/sys`
  v0.30.0; in `sroggrpc`/`srogecho` also `grpc` v1.71.0, `x/net` v0.36.0,
  `x/text` v0.22.0) — these are minimums, so builds with newer versions are
  unaffected. `srogotel` still requires Go 1.25, dictated by its OpenTelemetry
  dependencies (`otel` v1.44 / `log` v0.20).
- `srogelastic`, `sroggrpc`, and `srogecho` get their first prefixed module tags
  and now require `github.com/dvislobokov/srog v1.1.1` (previously the
  unresolvable `v0.0.0`), so they install cleanly outside the repository.

## [1.1.0] / srogotel/v1.1.0 — 2026-07-14

### Added — core

- **Pluggable sink types for the declarative config.** `srog.RegisterSinkType(name, factory)`
  lets external modules plug their sinks into a JSON/YAML `srog.Config`. A
  `SinkFactory` receives the full `Config` (to inherit logger-wide settings such
  as `timeFormat`) plus its `SinkSpec`, and returns the destination writer and
  its default format. Built-in type names (`console`, `file`, `stdout`,
  `stderr`) cannot be overridden.
- `SinkSpec.Options` (`"options"` in JSON/YAML) — a free-form object carrying
  type-specific settings for registered sinks, decoded via the new
  `SinkSpec.DecodeOptions(&v)` helper.
- Writers returned by a registered factory that implement `io.Closer` are now
  closed by `Logger.Close`, so network sinks flush on shutdown.

### Added — srogotel

- **OTLP log export to the OpenTelemetry Collector** via the OTel Logs Bridge
  API (`go.opentelemetry.io/otel/log`). The new `Sink` parses srog's JSON events
  and re-emits each one as an OTel log record, mapped onto the Logs Data Model
  (`time` → Timestamp, `level` → Severity, `message` → Body, `error`/`stack` →
  `exception.*`, `caller` → `code.*`, `@mt` → `log.template`); `trace_id`/`span_id`
  from `srogotel.Install()` become the record's trace context, so logs arrive
  already correlated with traces. Batching and retries are handled off the hot
  path by the SDK's `BatchProcessor`.
  - `srogotel.NewSink(ctx, cfg)` builds the sink; `srogotel.WithLogs(ctx, cfg, ...)`
    wires it straight into a logger:

    ```go
    // Reuse the global LoggerProvider configured next to traces/metrics:
    opt, sink, err := srogotel.WithLogs(ctx, srogotel.Config{})

    // ...or a specific provider:
    opt, sink, err := srogotel.WithLogs(ctx, srogotel.Config{Provider: lp})

    // ...or a private OTLP exporter with explicit parameters:
    opt, sink, err := srogotel.WithLogs(ctx, srogotel.Config{
        Endpoint: "collector:4317", Protocol: "grpc", Insecure: true,
        Headers:  map[string]string{"authorization": "Bearer ..."},
    })

    defer sink.Close()
    log := srog.MustNew(srog.WithConsole(), opt)
    ```

- **`Config.Attributes`** (optional) — static attributes stamped onto every
  record, for routing hints Collector exporters read (e.g. `data_stream.dataset`
  or `elasticsearch.index` for a dynamic Elasticsearch index). An event field
  with the same name wins over the static value.
- **`"otlp"` sink type in the declarative config.** Importing
  `github.com/dvislobokov/srog/srogotel` (a blank import suffices) registers it;
  an empty `options` reuses the global provider, `endpoint` builds a private
  exporter:

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

  The logger-wide `timeFormat` is inherited automatically (overridable in
  `options`). Leave the sink entry's `format` at its default — the sink parses
  srog's JSON events.
- New runnable example `examples/otel-logs`: global-provider reuse, private
  exporter, and config-driven variants against a local Collector.

### Changed — srogotel

- OpenTelemetry dependencies bumped to `otel v1.44.0` / `log v0.20.0`, adding
  `sdk/log` and the `otlploggrpc`/`otlploghttp` exporters.
- `go.mod` now requires `github.com/dvislobokov/srog v1.1.0` so the module
  resolves for external consumers.

## [1.0.0] — 2026-07-02

Initial stable release: Serilog-style message templates on zerolog, multi-sink
fan-out (console/file/writer) with per-sink levels and formats (JSON, console,
ECS, OTLP/JSON), rotation, sampling, async sinks, declarative JSON config,
context-scoped logging, and the sroghttp/sroggrpc/srogecho/srogotel/srogelastic
integration modules.
