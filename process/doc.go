// Package process runs external commands with cancellation and streamed
// lifecycle events.
//
// Run waits for completion, while Start returns a Process that can be waited
// on repeatedly, interrupted, or killed. Sinks receive stdout and stderr chunks
// as well as start and terminal events. Sink calls for a Process are serialized
// and must return promptly so output pipes continue to drain. WriterSink
// forwards output and Scrollback retains a bounded tail for later inspection.
package process
