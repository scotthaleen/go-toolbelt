// Package postgres provides a go-app component that owns a PostgreSQL
// database/sql connection pool backed by pgx.
//
// Store opens and verifies the pool, runs configured SQL statements and an
// optional Migrator during startup, and closes the pool during shutdown.
// Applications can use Migrator to integrate Goose or another versioned
// migration system without adding that dependency to this package.
package postgres
