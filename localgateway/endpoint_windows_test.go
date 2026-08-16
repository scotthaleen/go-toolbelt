//go:build windows

package localgateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func testEndpoint(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(`\\.\pipe\go-toolbelt-localgateway-%d-%d`, os.Getpid(), time.Now().UnixNano())
}

func endpointRemoved(string) bool { return true }

func TestSameWindowsSID(t *testing.T) {
	left, err := windows.StringToSid("S-1-5-21-1000-2000-3000-4000")
	if err != nil {
		t.Fatal(err)
	}
	equal, err := windows.StringToSid("S-1-5-21-1000-2000-3000-4000")
	if err != nil {
		t.Fatal(err)
	}
	different, err := windows.StringToSid("S-1-5-21-1000-2000-3000-4001")
	if err != nil {
		t.Fatal(err)
	}
	if !sameWindowsSID(left, equal) {
		t.Fatal("equal SIDs did not match")
	}
	if sameWindowsSID(left, different) {
		t.Fatal("different SIDs matched")
	}
	if sameWindowsSID(left, nil) {
		t.Fatal("nil SID matched")
	}
}

func TestGatewayRejectsAnonymousPipeClientAndRemainsUsable(t *testing.T) {
	endpoint := testEndpoint(t)
	server := New(DefaultConfig(endpoint), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	ctx, _ := gatewayContext(t)
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop(context.Background()) })

	dialCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := winio.DialPipeContext(dialCtx, endpoint)
	if err != nil {
		t.Fatalf("dial anonymous pipe client: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: local.gateway\r\n\r\n"); err == nil {
		var response [1]byte
		if _, err := conn.Read(response[:]); err == nil {
			t.Fatal("anonymous pipe client reached HTTP server")
		}
	}

	response, err := NewClient(endpoint, ClientConfig{}).Get(BaseURL)
	if err != nil {
		t.Fatalf("verified client request: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "ok" {
		t.Fatalf("verified client response = %q, err = %v", body, err)
	}
}
