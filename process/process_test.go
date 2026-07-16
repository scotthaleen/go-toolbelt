package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunStreamsStdoutAndStderr(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var events []EventType

	result := Run(
		context.Background(),
		testSpec("stdio"),
		WriterSink{Stdout: &stdout, Stderr: &stderr},
		SinkFunc(func(_ context.Context, evt Event) {
			events = append(events, evt.Type)
		}),
	)

	if result.Err != nil {
		t.Fatalf("run failed: %v", result.Err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", result.ExitCode)
	}
	if got := stdout.String(); got != "stdout\n" {
		t.Fatalf("stdout = %q, want stdout", got)
	}
	if got := stderr.String(); got != "stderr\n" {
		t.Fatalf("stderr = %q, want stderr", got)
	}
	if !reflect.DeepEqual(events[:1], []EventType{EventStarted}) {
		t.Fatalf("first event = %v, want started", events[:1])
	}
	if events[len(events)-1] != EventCompleted {
		t.Fatalf("last event = %v, want completed", events[len(events)-1])
	}
}

func TestRunDrainsLargeStdoutAndStderr(t *testing.T) {
	const streamSize = 4 << 20
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	result := Run(
		context.Background(),
		testSpec("large"),
		WriterSink{Stdout: &stdout, Stderr: &stderr},
	)
	if result.Err != nil {
		t.Fatalf("Run() error = %v", result.Err)
	}
	if stdout.Len() != streamSize || bytes.Count(stdout.Bytes(), []byte{'O'}) != streamSize {
		t.Fatalf("stdout length/content invalid: length = %d, want %d O bytes", stdout.Len(), streamSize)
	}
	if stderr.Len() != streamSize || bytes.Count(stderr.Bytes(), []byte{'E'}) != streamSize {
		t.Fatalf("stderr length/content invalid: length = %d, want %d E bytes", stderr.Len(), streamSize)
	}
}

func TestRunReportsFailure(t *testing.T) {
	result := Run(context.Background(), testSpec("fail"))

	if result.Err == nil {
		t.Fatal("expected error")
	}
	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
}

func TestRunCancelsOnContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := Run(ctx, testSpec("sleep"))

	if !result.Canceled {
		t.Fatal("expected canceled result")
	}
	if result.Err == nil {
		t.Fatal("expected cancellation error")
	}
	if !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("result error = %v, want context.DeadlineExceeded", result.Err)
	}
}

func TestRunSpecTimeoutPreservesDeadlineError(t *testing.T) {
	spec := testSpec("sleep")
	spec.Timeout = 100 * time.Millisecond
	result := Run(context.Background(), spec)
	if !result.Canceled {
		t.Fatal("expected canceled result")
	}
	if !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("result error = %v, want context.DeadlineExceeded", result.Err)
	}
}

func TestScrollbackKeepsTail(t *testing.T) {
	scrollback := NewScrollback(5)
	result := Run(context.Background(), testSpec("long"), scrollback)

	if result.Err != nil {
		t.Fatalf("run failed: %v", result.Err)
	}
	if got := scrollback.String(); got != "6789\n" {
		t.Fatalf("scrollback = %q, want tail", got)
	}
}

func TestMergedWriterSink(t *testing.T) {
	var merged bytes.Buffer
	result := Run(context.Background(), testSpec("stdio"), WriterSink{Merged: &merged})

	if result.Err != nil {
		t.Fatalf("run failed: %v", result.Err)
	}
	got := merged.String()
	if !strings.Contains(got, "stdout\n") || !strings.Contains(got, "stderr\n") {
		t.Fatalf("merged output = %q, want stdout and stderr", got)
	}
}

func TestStartRejectsEmptyPath(t *testing.T) {
	_, err := Start(context.Background(), Spec{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWaitCanBeCalledConcurrentlyAndRepeatedly(t *testing.T) {
	proc, err := Start(context.Background(), testSpec("stdio"))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	results := make(chan Result, 2)
	go func() { results <- proc.Wait() }()
	go func() { results <- proc.Wait() }()

	var first Result
	for i := range 2 {
		select {
		case result := <-results:
			if i == 0 {
				first = result
			} else if !reflect.DeepEqual(result, first) {
				t.Fatalf("concurrent Wait() results differ: %#v != %#v", result, first)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Wait() blocked")
		}
	}

	if got := proc.Wait(); !reflect.DeepEqual(got, first) {
		t.Fatalf("repeated Wait() = %#v, want %#v", got, first)
	}
}

func testSpec(mode string) Spec {
	return Spec{
		Path: os.Args[0],
		Args: []string{"-test.run=TestHelperProcess", "--", mode},
		Env:  map[string]string{"GO_TOOLBELT_PROCESS_HELPER": "1"},
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_TOOLBELT_PROCESS_HELPER") != "1" {
		return
	}
	args := os.Args
	mode := args[len(args)-1]
	switch mode {
	case "stdio":
		fmt.Fprintln(os.Stdout, "stdout")
		fmt.Fprintln(os.Stderr, "stderr")
		os.Exit(0)
	case "fail":
		os.Exit(7)
	case "sleep":
		if runtime.GOOS == "windows" {
			time.Sleep(5 * time.Second)
		}
		for {
			time.Sleep(time.Second)
		}
	case "long":
		fmt.Fprintln(os.Stdout, "0123456789")
		os.Exit(0)
	case "large":
		const streamSize = 4 << 20
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = os.Stdout.Write(bytes.Repeat([]byte{'O'}, streamSize))
		}()
		go func() {
			defer wg.Done()
			_, _ = os.Stderr.Write(bytes.Repeat([]byte{'E'}, streamSize))
		}()
		wg.Wait()
		os.Exit(0)
	default:
		os.Exit(2)
	}
}
