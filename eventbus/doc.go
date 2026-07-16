// Package eventbus provides typed, best-effort event delivery within a single
// process.
//
// A Broker fans events out to its subscribers without blocking publishers;
// events for a subscriber are dropped when that subscriber's buffer is full.
// Publisher adapters allow producers to depend on a small function type and
// can discard events or encode them as newline-delimited JSON.
package eventbus
