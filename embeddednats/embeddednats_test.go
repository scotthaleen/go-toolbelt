package embeddednats

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	server "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/scotthaleen/go-app"
)

func TestDefaultServerSupportsCoreNATSAndJetStream(t *testing.T) {
	s := New(Config{})
	startServer(t, s)

	if s.ClientURL() == "" {
		t.Fatal("ClientURL() is empty after Start")
	}
	if !s.NATSServer().JetStreamEnabled() {
		t.Fatal("JetStream is not enabled by default")
	}

	nc, err := s.Connect()
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(nc.Close)

	sub, err := nc.SubscribeSync("core.test")
	if err != nil {
		t.Fatalf("SubscribeSync() error = %v", err)
	}
	if err := nc.Publish("core.test", []byte("hello")); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	msg, err := sub.NextMsg(time.Second)
	if err != nil {
		t.Fatalf("NextMsg() error = %v", err)
	}
	if got := string(msg.Data); got != "hello" {
		t.Fatalf("message = %q, want hello", got)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream.New() error = %v", err)
	}
	stream, err := js.CreateStream(context.Background(), jetstream.StreamConfig{
		Name:     "MEMORY",
		Subjects: []string{"memory.>"},
		Storage:  jetstream.MemoryStorage,
	})
	if err != nil {
		t.Fatalf("CreateStream() error = %v", err)
	}
	if _, err := js.Publish(context.Background(), "memory.test", []byte("stored")); err != nil {
		t.Fatalf("JetStream Publish() error = %v", err)
	}
	stored, err := stream.GetLastMsgForSubject(context.Background(), "memory.test")
	if err != nil {
		t.Fatalf("GetLastMsgForSubject() error = %v", err)
	}
	if got := string(stored.Data); got != "stored" {
		t.Fatalf("stored message = %q, want stored", got)
	}
}

func TestTemporaryJetStreamStoreIsRemovedOnStop(t *testing.T) {
	s := New(Config{})
	ctx := serverContext(t)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	s.mu.RLock()
	tempDir := s.running.tempDir
	s.mu.RUnlock()
	if tempDir == "" {
		t.Fatal("temporary JetStream store is empty")
	}
	if _, err := os.Stat(tempDir); err != nil {
		t.Fatalf("Stat(%q) error = %v", tempDir, err)
	}

	stopServer(t, s)
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Fatalf("Stat(%q) error = %v, want not exist", tempDir, err)
	}
}

func TestConfiguredJetStreamStoreIsRetained(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "nats")
	s := New(Config{Options: &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  storeDir,
		NoLog:     true,
		NoSigs:    true,
	}})
	ctx := serverContext(t)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	stopServer(t, s)

	if _, err := os.Stat(storeDir); err != nil {
		t.Fatalf("configured store was not retained: %v", err)
	}
}

func TestInProcessConnectionWithoutListener(t *testing.T) {
	s := New(Config{Options: &server.Options{
		DontListen: true,
		JetStream:  true,
		NoLog:      true,
		NoSigs:     true,
	}})
	startServer(t, s)

	nc, err := s.ConnectInProcess()
	if err != nil {
		t.Fatalf("ConnectInProcess() error = %v", err)
	}
	nc.Close()
	if got := s.ClientURL(); got != "" {
		t.Fatalf("ClientURL() = %q, want empty string for server without listener", got)
	}
	if _, err := s.Connect(); err == nil {
		t.Fatal("Connect() error = nil for server without listener")
	}
}

func TestDefaultOptionsCanBeExtendedWithAuthentication(t *testing.T) {
	options := DefaultOptions()
	options.Username = "client"
	options.Password = "secret"
	s := New(Config{Options: options})
	startServer(t, s)

	if _, err := s.Connect(); err == nil {
		t.Fatal("Connect() error = nil without credentials")
	}
	nc, err := s.Connect(nats.UserInfo("client", "secret"))
	if err != nil {
		t.Fatalf("authenticated Connect() error = %v", err)
	}
	nc.Close()
}

func TestServerRejectsDuplicateStart(t *testing.T) {
	s := New(Config{})
	ctx := serverContext(t)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	if err := s.Start(ctx); err == nil {
		t.Fatal("second Start() error = nil, want already started error")
	}
}

func TestStopBeforeStart(t *testing.T) {
	s := New(Config{})
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if _, err := s.Connect(); err == nil {
		t.Fatal("Connect() error = nil before Start")
	}
	if _, err := s.ConnectInProcess(); err == nil {
		t.Fatal("ConnectInProcess() error = nil before Start")
	}
}

func startServer(t *testing.T, s *Server) {
	t.Helper()
	if err := s.Start(serverContext(t)); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { stopServer(t, s) })
}

func stopServer(t *testing.T, s *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func serverContext(t *testing.T) context.Context {
	t.Helper()
	runtime, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ctx := app.Register(context.Background(), app.RuntimeContext{Context: runtime})
	return app.Register(ctx, app.RequestShutdownFunc(func() {}))
}
