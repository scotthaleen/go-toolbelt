// Package httpserver provides a router-independent go-app component for a
// standard-library HTTP server.
//
// Server owns listener and http.Server lifecycle. Applications remain
// responsible for constructing the handler, routes, and middleware. Use
// WithLogger to share the logger passed to app.WithLogger. Unexpected serving
// failures request application shutdown and are returned from Server.Stop, so
// app.Run reports the failure.
package httpserver
