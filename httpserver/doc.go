// Package httpserver provides a router-independent go-app component for a
// standard-library HTTP server.
//
// Server owns listener and http.Server lifecycle. Applications remain
// responsible for constructing the handler, routes, and middleware.
package httpserver
