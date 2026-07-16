// Package echoserver provides go-app components for an Echo router and HTTP
// server.
//
// Capability and delivery components register routes through Router. Server
// owns only the listener and HTTP lifecycle, keeping the server independent of
// the capabilities exposed by those routes.
package echoserver
