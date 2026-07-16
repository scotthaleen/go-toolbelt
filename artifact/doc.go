// Package artifact provides file inspection and content-addressed filesystem
// staging.
//
// Inspect computes a file's SHA-256 digest and source metadata without moving
// it. Store.Put copies the file into a path derived from that digest:
//
//	<root>/<first two digest characters>/<full digest>
//
// Because the destination depends only on content, putting identical files
// reuses one stored copy regardless of their original names or locations. The
// returned Artifact retains source metadata, while StorePath identifies the
// durable copy.
//
// This package is useful as a building block for workflows that need to stage
// immutable inputs, cache generated files, or refer to files by content rather
// than a mutable source path. It does not manage a metadata index, garbage
// collection, remote storage, or application lifecycle, and it is not wired
// into another go-toolbelt component by default.
package artifact
