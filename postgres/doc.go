// Package postgres provides a go-app component that owns a PostgreSQL
// database/sql connection pool backed by pgx.
//
// Store opens and verifies the pool, applies configured SQL statements in
// order during startup, and closes the pool during shutdown. The migrations
// facility is intentionally minimal: statements run sequentially without a
// transaction, and migration versions are not tracked.
package postgres
