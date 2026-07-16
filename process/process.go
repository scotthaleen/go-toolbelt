package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

const defaultKillTimeout = 2 * time.Second

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

type EventType string

const (
	EventStarted   EventType = "started"
	EventChunk     EventType = "chunk"
	EventCompleted EventType = "completed"
	EventFailed    EventType = "failed"
	EventCanceled  EventType = "canceled"
)

type Spec struct {
	Path string
	Args []string
	Dir  string
	Env  map[string]string

	Timeout     time.Duration
	KillTimeout time.Duration
}

type Event struct {
	Type     EventType
	Stream   Stream
	Data     []byte
	PID      int
	ExitCode int
	Err      error
	Time     time.Time
}

type Result struct {
	PID       int
	ExitCode  int
	StartedAt time.Time
	EndedAt   time.Time
	Canceled  bool
	Err       error
}

type Sink interface {
	// HandleProcessEvent must return promptly so process output can continue to
	// drain. Calls for one Process are serialized and ordered per output stream.
	HandleProcessEvent(context.Context, Event)
}

type SinkFunc func(context.Context, Event)

func (f SinkFunc) HandleProcessEvent(ctx context.Context, evt Event) {
	f(ctx, evt)
}

type Process struct {
	cmd     *exec.Cmd
	sinks   []Sink
	sinkMu  sync.Mutex
	done    chan struct{}
	result  Result
	started time.Time
}

func Run(ctx context.Context, spec Spec, sinks ...Sink) Result {
	proc, err := Start(ctx, spec, sinks...)
	if err != nil {
		result := Result{ExitCode: -1, StartedAt: time.Now(), EndedAt: time.Now(), Err: err}
		emit(ctx, sinks, Event{Type: EventFailed, ExitCode: -1, Err: err, Time: result.EndedAt})
		return result
	}
	return proc.Wait()
}

func Start(ctx context.Context, spec Spec, sinks ...Sink) (*Process, error) {
	if spec.Path == "" {
		return nil, errors.New("process path is required")
	}
	if spec.KillTimeout <= 0 {
		spec.KillTimeout = defaultKillTimeout
	}

	cmdCtx := ctx
	var cancel context.CancelFunc
	if spec.Timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}

	cmd := exec.CommandContext(cmdCtx, spec.Path, spec.Args...)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = spec.KillTimeout
	cmd.Dir = spec.Dir
	cmd.Env = mergeEnv(os.Environ(), spec.Env)

	started := time.Now()
	proc := &Process{
		cmd:     cmd,
		sinks:   sinks,
		done:    make(chan struct{}),
		started: started,
	}
	outputReady := make(chan struct{})
	cmd.Stdout = processWriter{ctx: cmdCtx, proc: proc, stream: StreamStdout, ready: outputReady}
	cmd.Stderr = processWriter{ctx: cmdCtx, proc: proc, stream: StreamStderr, ready: outputReady}
	if err := cmd.Start(); err != nil {
		close(outputReady)
		cancel()
		return nil, fmt.Errorf("start process: %w", err)
	}

	emit(ctx, sinks, Event{Type: EventStarted, PID: cmd.Process.Pid, Time: started})
	close(outputReady)

	go proc.collect(cmdCtx, cancel)
	return proc, nil
}

func (p *Process) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *Process) Wait() Result {
	<-p.done
	return p.result
}

func (p *Process) Stop() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(os.Interrupt)
}

func (p *Process) Kill() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

func (p *Process) collect(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()

	err := p.cmd.Wait()

	ended := time.Now()
	result := Result{
		PID:       p.PID(),
		ExitCode:  p.cmd.ProcessState.ExitCode(),
		StartedAt: p.started,
		EndedAt:   ended,
		Canceled:  ctx.Err() != nil,
		Err:       err,
	}

	eventType := EventCompleted
	if result.Canceled {
		eventType = EventCanceled
	} else if err != nil || result.ExitCode != 0 {
		eventType = EventFailed
	}
	p.emit(ctx, Event{
		Type:     eventType,
		PID:      result.PID,
		ExitCode: result.ExitCode,
		Err:      err,
		Time:     ended,
	})
	p.result = result
	close(p.done)
}

type processWriter struct {
	ctx    context.Context
	proc   *Process
	stream Stream
	ready  <-chan struct{}
}

func (w processWriter) Write(data []byte) (int, error) {
	<-w.ready
	w.proc.emit(w.ctx, Event{
		Type:   EventChunk,
		Stream: w.stream,
		Data:   append([]byte(nil), data...),
		PID:    w.proc.PID(),
		Time:   time.Now(),
	})
	return len(data), nil
}

func (p *Process) emit(ctx context.Context, evt Event) {
	p.sinkMu.Lock()
	defer p.sinkMu.Unlock()
	emit(ctx, p.sinks, evt)
}

func emit(ctx context.Context, sinks []Sink, evt Event) {
	for _, sink := range sinks {
		if sink != nil {
			sink.HandleProcessEvent(ctx, evt)
		}
	}
}

func mergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	merged := make([]string, 0, len(base)+len(extra))
	seen := make(map[string]struct{}, len(extra))
	for key := range extra {
		seen[key] = struct{}{}
	}
	for _, item := range base {
		key, _, ok := splitEnv(item)
		if ok {
			if _, replace := seen[key]; replace {
				continue
			}
		}
		merged = append(merged, item)
	}
	for key, value := range extra {
		merged = append(merged, key+"="+value)
	}
	return merged
}

func splitEnv(item string) (string, string, bool) {
	for i := range item {
		if item[i] == '=' {
			return item[:i], item[i+1:], true
		}
	}
	return "", "", false
}

type WriterSink struct {
	Stdout io.Writer
	Stderr io.Writer
	Merged io.Writer
}

func (s WriterSink) HandleProcessEvent(_ context.Context, evt Event) {
	if evt.Type != EventChunk {
		return
	}
	if s.Merged != nil {
		_, _ = s.Merged.Write(evt.Data)
	}
	switch evt.Stream {
	case StreamStdout:
		if s.Stdout != nil {
			_, _ = s.Stdout.Write(evt.Data)
		}
	case StreamStderr:
		if s.Stderr != nil {
			_, _ = s.Stderr.Write(evt.Data)
		}
	}
}

type Scrollback struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
}

func NewScrollback(limit int) *Scrollback {
	return &Scrollback{limit: limit}
}

func (s *Scrollback) HandleProcessEvent(_ context.Context, evt Event) {
	if s == nil || s.limit <= 0 || evt.Type != EventChunk || len(evt.Data) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf.Write(evt.Data)
	if s.buf.Len() <= s.limit {
		return
	}
	data := append([]byte(nil), s.buf.Bytes()[s.buf.Len()-s.limit:]...)
	s.buf.Reset()
	_, _ = s.buf.Write(data)
}

func (s *Scrollback) String() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
