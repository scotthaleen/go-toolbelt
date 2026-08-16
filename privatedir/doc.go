// Package privatedir creates and validates application-owned private
// directories on Linux, macOS, and Windows.
//
// Private means that no untrusted principal has read or mutation access. On
// Windows, SYSTEM and the built-in Administrators group are trusted exceptions.
// Inheritable access entries for other principals are rejected because they can
// affect application-owned children.
//
// The package checks the final path object. The caller is responsible for
// choosing a stable path on a local filesystem. The cooperative threat model
// does not protect against a malicious process running as the same OS user.
package privatedir
