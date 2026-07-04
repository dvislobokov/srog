# fullexample — clean-architecture service with srog + OpenTelemetry

A small but complete **orders API** that shows how to run srog in a real service:
clean architecture, request-scoped logging, and OpenTelemetry traces with
`trace_id`/`span_id` correlated into every log line (via `srogotel`).

```bash
go run ./cmd/api
# in another shell:
curl -si -H 'X-Correlation-Id: corr-777' -d '{"customer":"neo","amount_cents":1999}' localhost:8080/orders
curl -s  localhost:8080/orders/<id-from-above>
```

Logs (JSON) go to **stdout**, spans go to **stderr** — read them side by side and
match by `trace_id`.

## Architecture

Dependencies point inward. Nothing in an inner layer imports an outer one.

```
        ┌────────────────────────── cmd/api (composition root) ──────────────────────────┐
        │  builds observability, wires the graph, assembles middleware, runs the server   │
        └───────────────┬───────────────────────────────────────────────┬────────────────┘
                        │ depends on                                     │ depends on
             ┌──────────▼──────────┐                          ┌──────────▼──────────┐
  inbound →  │  adapter/httpapi    │                          │   internal/platform │  ← infrastructure
             │  handlers, DTOs     │                          │  logging / tracing  │
             └──────────┬──────────┘                          │    / middleware     │
                        │ calls                               └─────────────────────┘
             ┌──────────▼──────────┐        implements
             │     internal/app    │◄───────────────────────┐
             │  use cases (ports)  │                        │
             └──────────┬──────────┘             ┌──────────┴──────────┐
                        │ depends on             │  adapter/memrepo    │  ← outbound
             ┌──────────▼──────────┐             │  in-memory repo     │
             │   internal/domain   │◄────────────┤  (domain port impl) │
             │ entities + ports    │  implements └─────────────────────┘
             │  (no deps at all)   │
             └─────────────────────┘
```

| Layer | Package | Rule |
|-------|---------|------|
| Domain | `internal/domain` | Entities + ports. **Zero** dependencies (not even srog). |
| Use cases | `internal/app` | Orchestrates domain; logs via `srog.Ctx`, spans via an injected tracer. |
| Inbound adapter | `internal/adapter/httpapi` | HTTP ⇄ use cases; maps domain errors to status codes. |
| Outbound adapter | `internal/adapter/memrepo` | Implements the `OrderRepository` port. |
| Infrastructure | `internal/platform` | srog/OTel construction + middleware. |
| Composition root | `cmd/api` | The only place that knows all concrete types. |

## How observability flows

1. `cmd/api` builds the logger, the OTel tracer provider, and calls
   `srogotel.Install()` once so the active span's `trace_id`/`span_id` are
   attached to every context-scoped log.
2. The middleware chain (outer → inner): **otelhttp** creates the server span and
   puts it in the request context → **sroghttp** assigns a `CorrelationId` and
   injects a request-scoped logger.
3. Handlers and use cases log through the **package directly** — `srog.InfoCtx(ctx, …)`,
   `srog.WarningCtx`, `srog.ErrorCtx`, `srog.DebugCtx`. These resolve the
   request-scoped logger from the context (falling back to the default set with
   `srog.SetDefault`), so there is no logger variable, field, or parameter
   anywhere in the business code — yet every line carries `CorrelationId` **and**
   the trace IDs.

Because the access-log line is emitted on the parent (server) span and the
business lines on child spans, they all share one `trace_id` — so logs and traces
join up in Kibana/Jaeger/Tempo.

## Why the split pays off

`internal/app/order_service_test.go` unit-tests the use cases with a stub
repository and a no-op tracer — **no HTTP server, no tracer backend, no
database.** That is the whole point of keeping the domain and use cases free of
infrastructure.

## Swapping infrastructure

- **Persistence:** implement `domain.OrderRepository` in a new `adapter/pgrepo`
  and change one line in `cmd/api`. The domain and use cases are untouched.
- **Trace backend:** replace `stdouttrace` in `internal/platform/tracing.go` with
  an OTLP exporter to your collector — nothing else changes.
- **Log shipping:** add `srog.WithFile(..., srog.Async(...))` or `srog.AsECS()` in
  `internal/platform/logging.go`.
