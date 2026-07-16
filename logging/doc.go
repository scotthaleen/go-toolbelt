// Package logging provides opinionated log/slog setup helpers.
//
// It maps command-line-style verbosity counts to slog levels, selects Tint
// output for terminals and JSON otherwise, normalizes timestamps, and can add
// a request ID stored in a context to each record. Callers can explicitly
// select Tint, text, or JSON and can provide additional attribute rewriting.
// The default output is os.Stdout. Constructors return an error for unsupported
// formats.
package logging
