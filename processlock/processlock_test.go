package processlock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/scotthaleen/go-app"
)

func TestLockExcludesAnotherOwnerAndReleases(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("process lock unsupported")
	}
	path := filepath.Join(privateTempDir(t), "process.lock")
	first := New(Config{Path: path})
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Stop(context.Background()) })

	second := New(Config{Path: path})
	if err := second.Start(context.Background()); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Start error = %v, want ErrLocked", err)
	}
	if err := first.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Start(context.Background()); err != nil {
		t.Fatalf("Start after release: %v", err)
	}
	if err := second.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStartValidatesState(t *testing.T) {
	lock := New(Config{})
	if err := lock.Start(context.Background()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty-path error = %v, want ErrInvalidConfig", err)
	}

	lock = New(Config{Path: filepath.Join(privateTempDir(t), "process.lock")})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lock.Start(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Start error = %v", err)
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		if err := lock.Start(context.Background()); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("unsupported Start error = %v, want ErrUnsupported", err)
		}
		return
	}
	if err := lock.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lock.Start(context.Background()); !errors.Is(err, ErrStarted) {
		t.Fatalf("second Start error = %v, want ErrStarted", err)
	}
	if err := lock.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lock.Stop(context.Background()); err != nil {
		t.Fatalf("idempotent Stop: %v", err)
	}
}

func TestConcurrentStartIsSerialized(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("process lock unsupported")
	}
	lock := New(Config{Path: filepath.Join(privateTempDir(t), "process.lock")})
	const attempts = 16
	errs := make(chan error, attempts)
	var ready sync.WaitGroup
	ready.Add(attempts)
	start := make(chan struct{})
	for range attempts {
		go func() {
			ready.Done()
			<-start
			errs <- lock.Start(context.Background())
		}()
	}
	ready.Wait()
	close(start)

	succeeded := 0
	for range attempts {
		err := <-errs
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrStarted):
		default:
			t.Fatalf("Start error = %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful starts = %d, want 1", succeeded)
	}
	if err := lock.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentStopIsIdempotent(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("process lock unsupported")
	}
	lock := New(Config{Path: filepath.Join(privateTempDir(t), "process.lock")})
	if err := lock.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	const attempts = 16
	errs := make(chan error, attempts)
	for range attempts {
		go func() { errs <- lock.Stop(context.Background()) }()
	}
	for range attempts {
		if err := <-errs; err != nil {
			t.Fatalf("Stop error = %v", err)
		}
	}
}

func TestStopPreservesIndeterminateRelease(t *testing.T) {
	releaseErr := errors.New("release failed")
	lock := New(Config{Path: "unused"})
	lock.handle = fakeHandle{released: false, err: releaseErr}
	if err := lock.Stop(context.Background()); !errors.Is(err, releaseErr) {
		t.Fatalf("Stop error = %v", err)
	}
	if lock.handle == nil {
		t.Fatal("indeterminate release discarded handle")
	}

	lock.handle = fakeHandle{released: true, err: releaseErr}
	if err := lock.Stop(context.Background()); !errors.Is(err, releaseErr) {
		t.Fatalf("Stop error = %v", err)
	}
	if lock.handle != nil {
		t.Fatal("definite release retained handle")
	}
}

func TestStartupRollbackReleasesLock(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("process lock unsupported")
	}
	path := filepath.Join(privateTempDir(t), "process.lock")
	lock := New(Config{Path: path})
	failure := app.NewComponent(
		app.WithName("failing component"),
		app.WithOnStart(func(context.Context) error { return errors.New("start failed") }),
	)
	application := app.New(
		context.Background(),
		app.WithSignalHandling(false),
		app.WithSequentialStartup(app.Managed(lock), app.Managed(failure)),
	)
	if err := application.Start(context.Background()); err == nil {
		t.Fatal("application startup succeeded")
	}

	competitor := New(Config{Path: path})
	if err := competitor.Start(context.Background()); err != nil {
		t.Fatalf("lock remained held after startup rollback: %v", err)
	}
	if err := competitor.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSubprocessContentionAndCrashRelease(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("process lock unsupported")
	}
	path := filepath.Join(privateTempDir(t), "process.lock")
	first := New(Config{Path: path})
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	cmd := helperCommand("contend", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("contention helper: %v: %s", err, output)
	}
	if err := first.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	cmd = helperCommand("hold", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "locked" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("helper did not acquire lock: %q, %v", scanner.Text(), scanner.Err())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()

	afterCrash := New(Config{Path: path})
	if err := afterCrash.Start(context.Background()); err != nil {
		t.Fatalf("lock remained held after process exit: %v", err)
	}
	if err := afterCrash.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestProcessLockHelper(t *testing.T) {
	mode := os.Getenv("GO_TOOLBELT_PROCESSLOCK_HELPER")
	if mode == "" {
		return
	}
	lock := New(Config{Path: os.Getenv("GO_TOOLBELT_PROCESSLOCK_PATH")})
	err := lock.Start(context.Background())
	switch mode {
	case "contend":
		if errors.Is(err, ErrLocked) {
			os.Exit(0)
		}
		os.Exit(2)
	case "hold":
		if err != nil {
			os.Exit(3)
		}
		fmt.Println("locked")
		select {}
	default:
		os.Exit(4)
	}
}

func helperCommand(mode, path string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessLockHelper$")
	cmd.Env = append(os.Environ(), "GO_TOOLBELT_PROCESSLOCK_HELPER="+mode, "GO_TOOLBELT_PROCESSLOCK_PATH="+path)
	return cmd
}

type fakeHandle struct {
	released bool
	err      error
}

func (h fakeHandle) release() (bool, error) {
	return h.released, h.err
}

func TestComponentName(t *testing.T) {
	lock := New(Config{Path: "unused", Name: "agent lock"})
	if got := lock.Component().Name(); got != "agent lock" {
		t.Fatalf("component name = %q", got)
	}
}
