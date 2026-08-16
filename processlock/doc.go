// Package processlock provides a lifecycle-owned exclusive process lock for
// Linux, macOS, and Windows.
//
// The caller must place the lock in a private, stable, local-filesystem
// directory owned by the current OS user. Other users must not be able to
// mutate that directory. On Windows, SYSTEM and the built-in Administrators
// group are trusted exceptions. The package validates the final parent
// directory and lock object ownership and access. The caller remains
// responsible for using a local filesystem and a stable path. Do not remove or
// replace the lock file while an application may be running: Unix locks protect
// an inode rather than a pathname.
//
// The lock coordinates cooperative applications and is not a security boundary
// against another malicious process running as the same OS user. It is not a
// distributed lock.
package processlock
