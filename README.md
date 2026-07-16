# go-toolbelt

Companion library for [`go-app`](https://github.com/scotthaleen/go-app), with practical components, adapters, and recipes.

Packages here may evolve faster than the stable `go-app` lifecycle core as application patterns settle.

## Packages

- `ai`: Charm Fantasy-backed AI client component.
- `artifact`: content hash metadata and content-addressed filesystem staging.
- `echoserver`: Echo router and HTTP server components.
- `eventbus`: typed, best-effort in-process event fan-out.
- `httpserver`: router-independent standard-library HTTP server lifecycle.
- `logging`: `log/slog` setup helpers, including `-v/-vv/-vvv` style verbosity mapping.
- `postgres`: a small `go-app` component that owns a Postgres `*sql.DB` lifecycle through `pgx`.
- `process`: streaming process execution with event sinks and cancellation.
- `sqlite`: a small `go-app` component that owns a SQLite `*sql.DB` lifecycle.

The SQLite and PostgreSQL components accept application-owned migration
callbacks, allowing integration with Goose or another versioned migration
system without requiring it as a toolbelt dependency. Logging supports Tint,
plain text, and JSON, with automatic Tint output on terminals and JSON
otherwise.

## Documentation

- `README.md`: package overview and runnable examples.
- `SKILL.md`: portable usage guidance for agents using these packages.

## Examples

```sh
go run ./examples/sqlite -vvv
go run ./examples/advanced-jobs -vvv
OPENAI_API_KEY=... go run ./examples/ai-cli "write a haiku about app lifecycle"
```

Try the advanced jobs example with:

```sh
curl -X POST http://localhost:8082/api/jobs -d '{"duration":"10s"}' -H 'content-type: application/json'
curl http://localhost:8082/api/jobs
curl -X DELETE http://localhost:8082/api/jobs/1
curl -X POST http://localhost:8082/shutdown
```

The advanced jobs example demonstrates a capability component plus delivery adapter components:

- `JobManager` owns job state and goroutine lifecycle.
- `JobHTTP` exposes jobs over Echo routes.
- `JobCLI` exposes the same jobs over stdin/stdout commands.
- `echoserver.Router` owns the shared Echo router.
- `echoserver.Server` only listens and serves the router.

The server does not know about the job manager. The adapters resolve the dependencies they need during component startup, then runtime handlers use captured fields rather than reaching back into the app registry.

## Development

This module targets Go 1.26.4. Consumers using an older Go version must update
their declared version or enable the corresponding Go toolchain before adopting
the module.

The toolbelt intentionally remains a single Go module. Importing a subset of
its packages may therefore add dependencies used by other packages to module
resolution, although the Go linker excludes unused package code from binaries.

```sh
task fmt
task test
task vet
task check
```

`task fmt` runs `gofumpt` through `go run`, so `task` is the only extra command expected locally.

## go-app Dependency

Components and examples use the published `github.com/scotthaleen/go-app` module, currently at `v1.0.0`.
