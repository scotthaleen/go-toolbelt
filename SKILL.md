---
name: go-toolbelt
description: Use when building Go applications with companion components, delivery adapters, and helpers from github.com/scotthaleen/go-toolbelt alongside github.com/scotthaleen/go-app.
---

# Use go-toolbelt

Use this skill when building applications with `github.com/scotthaleen/go-toolbelt`, the companion library for `github.com/scotthaleen/go-app`.

## Core Pattern

Model application behavior as:

```txt
capability component + delivery adapter component(s)
```

Capability components:

- Own resources and state.
- Expose normal Go methods.
- Do not know about HTTP, CLI, NATS, TUI, or IPC unless that is their actual capability.

Delivery adapters:

- Depend on one or more capability components.
- Register routes, commands, subscriptions, or handlers in `Start(ctx)`.
- Store resolved dependencies in fields.
- Use `app.RuntimeContext` for app-lifetime goroutines; do not retain startup `ctx` for runtime loops.
- Keep runtime handlers free of app registry lookups.

## go-app Usage

```go
a := app.New(ctx,
	app.WithSequentialStartup(
		app.Registered(capability),
		app.Registered(router),
		app.Managed(httpAdapter),
		app.Managed(cliAdapter),
		app.Managed(server),
	),
)
```

Startup shape is explicit and caller-owned. Use `app.WithConcurrentStartup` only for independent components that can safely start together. Do not add dependency graph solving here.

## Examples

- `examples/sqlite`: minimal lifecycle component usage.
- `examples/advanced-jobs`: capability plus HTTP and CLI adapters.
- `examples/ai-cli`: AI component wrapping Charm Fantasy for a single CLI prompt.

## Infrastructure Components

- Use `sqlite.Config.Migrate` or `postgres.Config.Migrate` to integrate an
  application-owned versioned migration system such as Goose.
- Use `httpserver` when an application supplies a standard `http.Handler`; the
  application continues to own routing and middleware. Pass the application's
  logger through `httpserver.WithLogger` as well as `app.WithLogger`.
- Use `logging.FormatAuto` for Tint on terminals and JSON elsewhere, or select
  `FormatTint`, `FormatText`, or `FormatJSON` explicitly.
