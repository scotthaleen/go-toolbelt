// Package embeddednats provides a lifecycle-managed NATS server for local
// applications, integration tests, and single-binary development tools.
//
// The default server listens on a random loopback port and enables JetStream.
// Its JetStream metadata uses a temporary directory because NATS currently
// requires a writable store directory even when streams use memory storage.
// Supply server.Options.StoreDir to retain JetStream data across restarts.
//
// Use ClientURL or Connect for the same TCP client path used with an external
// NATS deployment. ConnectInProcess is available when a test should avoid
// opening a network socket.
package embeddednats
