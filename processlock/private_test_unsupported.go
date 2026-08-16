//go:build !linux && !darwin && !windows

package processlock

import "testing"

func privateTempDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}
