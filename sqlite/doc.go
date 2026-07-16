// Package sqlite provides a go-app component that owns a SQLite database/sql
// connection pool backed by modernc.org/sqlite.
//
// Store opens and verifies the database, applies configured SQL statements in
// order during startup, and closes the pool during shutdown. By default it
// uses a private in-memory database and one open connection. The migrations
// facility is intentionally minimal: statements run sequentially without a
// transaction, and migration versions are not tracked.
package sqlite
