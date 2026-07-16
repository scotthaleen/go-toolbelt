package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/scotthaleen/go-app"
	_ "modernc.org/sqlite"
)

type Config struct {
	DSN          string
	Migrations   []string
	Migrate      Migrator
	MaxOpenConns int
	MaxIdleConns int
	MaxIdleTime  time.Duration
}

// Migrator initializes or upgrades an opened database during startup.
type Migrator func(context.Context, *sql.DB) error

type Store struct {
	cfg     Config
	mu      sync.RWMutex
	db      *sql.DB
	closing *closeState
	open    func(string, string) (*sql.DB, error)
}

type closeState struct {
	done chan struct{}
	err  error
}

func New(cfg Config) *Store {
	if cfg.DSN == "" {
		cfg.DSN = ":memory:"
	}
	if cfg.MaxOpenConns == 0 {
		cfg.MaxOpenConns = 1
	}
	return &Store{cfg: cfg, open: sql.Open}
}

func (s *Store) Component() *app.Component {
	return app.NewComponent(
		app.WithName("sqlite"),
		app.WithOnStart(s.Start),
		app.WithOnStop(s.Stop),
	)
}

func (s *Store) DB() *sql.DB {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db
}

func (s *Store) Start(ctx context.Context) error {
	if s.cfg.DSN == ":memory:" && (s.cfg.MaxOpenConns != 1 || s.cfg.MaxIdleTime > 0) {
		return errors.New("sqlite :memory: requires one open connection and no idle timeout")
	}
	s.mu.Lock()
	if s.db != nil {
		s.mu.Unlock()
		return errors.New("sqlite already started")
	}
	if s.closing != nil {
		select {
		case <-s.closing.done:
			closeErr := s.closing.err
			s.closing = nil
			if closeErr != nil {
				s.mu.Unlock()
				return fmt.Errorf("previous sqlite shutdown: %w", closeErr)
			}
		default:
			s.mu.Unlock()
			return errors.New("sqlite shutdown in progress")
		}
	}
	s.mu.Unlock()
	db, err := s.open("sqlite", s.cfg.DSN)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}

	if s.cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(s.cfg.MaxOpenConns)
	}
	if s.cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(s.cfg.MaxIdleConns)
	}
	if s.cfg.MaxIdleTime > 0 {
		db.SetConnMaxIdleTime(s.cfg.MaxIdleTime)
	}

	if err := db.PingContext(ctx); err != nil {
		return closeAfterStartError(db, fmt.Errorf("ping sqlite: %w", err))
	}

	if err := runMigrations(ctx, db, s.cfg.Migrations); err != nil {
		return closeAfterStartError(db, fmt.Errorf("run sqlite migrations: %w", err))
	}
	if s.cfg.Migrate != nil {
		if err := s.cfg.Migrate(ctx, db); err != nil {
			return closeAfterStartError(db, fmt.Errorf("migrate sqlite: %w", err))
		}
	}

	s.mu.Lock()
	s.db = db
	s.mu.Unlock()
	return nil
}

func runMigrations(ctx context.Context, db *sql.DB, migrations []string) error {
	if len(migrations) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	for _, migration := range migrations {
		if _, err := tx.ExecContext(ctx, migration); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func closeAfterStartError(db *sql.DB, startErr error) error {
	closeErr := db.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close sqlite after startup failure: %w", closeErr)
	}
	return errors.Join(startErr, closeErr)
}

func (s *Store) Stop(ctx context.Context) error {
	s.mu.Lock()
	closing := s.closing
	if closing == nil {
		if s.db == nil {
			s.mu.Unlock()
			return nil
		}
		db := s.db
		s.db = nil
		closing = &closeState{done: make(chan struct{})}
		s.closing = closing
		s.mu.Unlock()
		go func() {
			closing.err = db.Close()
			close(closing.done)
		}()
	} else {
		s.mu.Unlock()
	}

	select {
	case <-closing.done:
		s.mu.Lock()
		if s.closing == closing {
			s.closing = nil
		}
		s.mu.Unlock()
		return errors.Join(closing.err, ctx.Err())
	case <-ctx.Done():
		return ctx.Err()
	}
}
