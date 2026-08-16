//go:build !linux && !darwin && !windows

package privatedir

import "testing"

func privateTempDir(t *testing.T) string { return t.TempDir() }
