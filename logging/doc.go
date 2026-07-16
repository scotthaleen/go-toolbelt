// Package logging provides opinionated log/slog setup helpers.
//
// It maps command-line-style verbosity counts to slog levels, selects text
// output for terminals and JSON otherwise, normalizes timestamps, and can add
// a request ID stored in a context to each record.
package logging
