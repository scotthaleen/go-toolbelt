// Package localgateway serves standard HTTP handlers over protected local IPC.
//
// Linux and macOS use user-owned Unix domain sockets and verify the effective
// user of every peer. Windows restricts named pipes to the current user; servers
// verify the connected client's user SID, and clients verify the connected pipe
// object's owner SID. Windows clients built with versions before v1.10 request
// anonymous impersonation and are rejected by v1.10 and newer servers, so upgrade
// both ends together. Connections are admitted through a configurable bounded
// listener before net/http starts serving them.
// The default connection limit is 64 and the supported maximum is 4096. Use
// WithMaxConnections to select another supported limit; invalid values use the
// default.
//
// Server.Stop prevents new application handler entry, closes the HTTP server,
// and waits for all entered handlers to exit. Handlers must observe their
// request contexts. A handler that ignores cancellation can cause Stop to wait
// beyond its context deadline because handler-exit safety takes precedence. An
// entered handler must not call or wait for Server.Stop: it should request
// shutdown and return, observing request-context cancellation as needed.
//
// The package owns transport and lifecycle only; routes, message schemas, body
// limits, and application authorization remain application concerns.
package localgateway
