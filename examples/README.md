# srog examples

Each example is its own Go module (with a `replace` back to the repo) so its
dependencies stay isolated from the core library. Run any of them with:

```bash
cd examples/<name>
go mod tidy
go run .
```

| Example | What it shows | Extra modules |
|---------|---------------|---------------|
| [`basic`](basic) | Levels, message templates (named/positional/`@`/`$`, format & alignment), enrichment, error stacks | — |
| [`config`](config) | Building a logger from a declarative JSON `srog.Config`; console + rotating ECS file | — |
| [`formats`](formats) | Every output variant from one `logging.json`: console (stdout/stderr), raw JSON stream, and `json`/`ecs`/`otel` files with rotation | — |
| [`nethttp`](nethttp) | **Logger injected via middleware into the request context, used in any handler** (stdlib `net/http` + `sroghttp`) | — |
| [`echo`](echo) | Same request-scoped pattern with the Echo framework: `srogecho.Middleware`, `Recover`, `From` | `srogecho` |
| [`grpc`](grpc) | gRPC server interceptor injecting a request-scoped logger; handler reads it from the call context | `sroggrpc` |
| [`otel`](otel) | OpenTelemetry trace/log correlation — `trace_id`/`span_id` flow into logs via `srog.Ctx` | `srogotel` |
| [`shared-convention`](shared-convention) | One `platformlog` package pins the correlation-id field/header/metadata for every service; the same id flows HTTP → gRPC with no handler changes | `sroggrpc` |
| [`elk`](elk) | Direct, non-blocking shipping to Elasticsearch via `srogelastic` | `srogelastic` |

The `nethttp` and `echo` examples both demonstrate the core idea: middleware
enriches a logger with a `RequestId`, stashes it in `context.Context`, and
handlers retrieve it with `srog.FromContext(ctx)` / `srog.Ctx(ctx)` /
`srog.InfoCtx(ctx, ...)` — no logger is ever passed as a function argument.
