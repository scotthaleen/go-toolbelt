package processlock

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/scotthaleen/go-app"
)

var (
	// ErrInvalidConfig identifies a missing or invalid required configuration.
	ErrInvalidConfig = errors.New("invalid process lock configuration")
	// ErrLocked means another process currently owns the selected lock.
	ErrLocked = errors.New("process lock is held")
	// ErrStarted means Start was called again before a successful Stop.
	ErrStarted = errors.New("process lock already started")
	// ErrUnsafePath identifies a lock path that does not satisfy the private,
	// current-user-owned filesystem contract.
	ErrUnsafePath = errors.New("unsafe process lock path")
	// ErrUnsupported means the current platform is not Linux, macOS, or Windows.
	ErrUnsupported = errors.New("process lock is unsupported")
)

// Config describes one process lock. Path must be within a private, stable,
// local-filesystem directory owned by the current user. Name is optional and is
// used for go-app lifecycle diagnostics.
type Config struct {
	Path string
	Name string
}

type lockHandle interface {
	release() (bool, error)
}

// Lock is a restartable go-app lifecycle component. A successful Stop releases
// the OS lock but intentionally leaves the lock file in place.
//
// Lifecycle methods are serialized. If an OS release result is indeterminate,
// Stop returns an error and retains the handle so a later Stop can retry.
type Lock struct {
	cfg    Config
	mu     sync.Mutex
	handle lockHandle
}

// New constructs a stopped lock. Configuration is validated by Start so New
// can match the constructor shape of other lifecycle components.
func New(cfg Config) *Lock {
	if cfg.Name == "" {
		cfg.Name = "process lock"
	}
	return &Lock{cfg: cfg}
}

// Component exposes the lock as a go-app component.
func (l *Lock) Component() *app.Component {
	return app.NewComponent(
		app.WithName(l.cfg.Name),
		app.WithOnStart(l.Start),
		app.WithOnStop(l.Stop),
	)
}

// Start validates the configured path and attempts one nonblocking exclusive
// acquisition. It returns ErrLocked when another process owns the lock.
func (l *Lock) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.cfg.Path == "" {
		return fmt.Errorf("%w: path is required", ErrInvalidConfig)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.handle != nil {
		return ErrStarted
	}
	handle, err := acquire(l.cfg.Path)
	if err != nil {
		return fmt.Errorf("acquire process lock %q: %w", l.cfg.Path, err)
	}
	l.handle = handle
	return nil
}

// Stop releases the lock. It is idempotent after a definite release. When
// release is indeterminate, Stop retains the handle and can be retried.
func (l *Lock) Stop(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.handle == nil {
		return nil
	}
	released, err := l.handle.release()
	if released {
		l.handle = nil
	}
	if err != nil {
		return fmt.Errorf("release process lock %q: %w", l.cfg.Path, err)
	}
	return nil
}
