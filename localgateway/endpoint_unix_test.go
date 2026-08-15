//go:build linux || darwin

package localgateway

import (
	"os"
	"path/filepath"
	"testing"
)

func testEndpoint(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "localgateway-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "gateway.sock")
}

func endpointRemoved(endpoint string) bool {
	_, err := os.Lstat(endpoint)
	return os.IsNotExist(err)
}
