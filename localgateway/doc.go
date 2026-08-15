// Package localgateway serves standard HTTP handlers over protected local IPC.
//
// Linux and macOS use user-owned Unix domain sockets and verify the effective
// user of every peer. Windows uses current-user-only named pipes. The package
// owns transport and lifecycle only; routes, message schemas, body limits, and
// application authorization remain application concerns.
package localgateway
