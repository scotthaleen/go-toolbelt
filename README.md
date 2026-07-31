# go-toolbelt

Companion library for [`go-app`](https://github.com/scotthaleen/go-app), with practical components, adapters, and recipes.

Packages here may evolve faster than the stable `go-app` lifecycle core as application patterns settle.

## Packages

- `ai`: Charm Fantasy-backed AI client component.
- `artifact`: content hash metadata and content-addressed filesystem staging.
- `echoserver`: Echo router and HTTP server components.
- `embeddednats`: embedded NATS server with ephemeral JetStream support.
- `eventbus`: typed, best-effort in-process event fan-out.
- `httpserver`: router-independent standard-library HTTP server lifecycle.
- `logging`: `log/slog` setup helpers, including `-v/-vv/-vvv` style verbosity mapping.
- `oidcverifier`: lifecycle-managed OIDC discovery and ID-token verification.
- `postgres`: a small `go-app` component that owns a Postgres `*sql.DB` lifecycle through `pgx`.
- `process`: streaming process execution with event sinks and cancellation.
- `sqlite`: a small `go-app` component that owns a SQLite `*sql.DB` lifecycle.

The SQLite and PostgreSQL components accept application-owned migration
callbacks, allowing integration with Goose or another versioned migration
system without requiring it as a toolbelt dependency. Logging supports Tint,
plain text, and JSON, with automatic Tint output on terminals and JSON
otherwise. The OIDC verifier handles protocol validation and key rotation while
leaving provider-specific identity authorization to applications.

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

## Embedded NATS

The `embeddednats` package runs a NATS server in the application process. The
default server enables JetStream and listens on a random loopback port:

```go
broker := embeddednats.New(embeddednats.Config{})

a := app.New(ctx,
	app.WithSequentialStartup(
		app.Managed(broker),
		app.Managed(natsAdapter),
	),
)
```

The dependent component can call `broker.Connect()` during its `Start` method,
or it can pass `broker.ClientURL()` to code that already accepts a NATS URL.
Drain client connections before the broker component stops. Production code can
use the same client path with the URL of an external NATS cluster.

NATS requires a writable JetStream store directory, including when all streams
use memory storage. If `server.Options.StoreDir` is empty, `embeddednats`
creates a private temporary directory and removes it after shutdown. Configure
streams with `jetstream.MemoryStorage` to keep message data in memory. Supply a
`StoreDir` to retain file-backed streams across restarts.

Pass `server.Options` through `embeddednats.Config` to configure a fixed port,
resource limits, authentication, TLS, or other NATS server behavior. Use
`embeddednats.DefaultOptions()` as the starting point when you want to retain
the package defaults. Set `DontListen` and use `ConnectInProcess` when a test
must not open a TCP socket. The in-process path does not test TCP or TLS
transport behavior.

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
