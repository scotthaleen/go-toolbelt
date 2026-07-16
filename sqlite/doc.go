// Package sqlite provides a go-app component that owns a SQLite database/sql
// connection pool backed by modernc.org/sqlite.
//
// Store opens and verifies the database, runs configured SQL statements and an
// optional Migrator during startup, and closes the pool during shutdown. By
// default it uses a private in-memory database and one open connection.
// Applications can use Migrator to integrate Goose or another versioned
// migration system without adding that dependency to this package.
package sqlite
