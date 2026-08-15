//go:build windows

package localgateway

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func testEndpoint(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(`\\.\pipe\go-toolbelt-localgateway-%d-%d`, os.Getpid(), time.Now().UnixNano())
}

func endpointRemoved(string) bool { return true }
