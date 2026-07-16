package sqlite

import (
	"context"
	"database/sql"
	"fmt"
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
	cfg Config
	db  *sql.DB
}

func New(cfg Config) *Store {
	if cfg.DSN == "" {
		cfg.DSN = ":memory:"
	}
	if cfg.MaxOpenConns == 0 {
		cfg.MaxOpenConns = 1
	}
	return &Store{cfg: cfg}
}

func (s *Store) Component() *app.Component {
	return app.NewComponent(
		app.WithName("sqlite"),
		app.WithOnStart(s.Start),
		app.WithOnStop(s.Stop),
	)
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Start(ctx context.Context) error {
	db, err := sql.Open("sqlite", s.cfg.DSN)
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
		_ = db.Close()
		return fmt.Errorf("ping sqlite: %w", err)
	}

	for _, migration := range s.cfg.Migrations {
		if _, err := db.ExecContext(ctx, migration); err != nil {
			_ = db.Close()
			return fmt.Errorf("run sqlite migration: %w", err)
		}
	}
	if s.cfg.Migrate != nil {
		if err := s.cfg.Migrate(ctx, db); err != nil {
			_ = db.Close()
			return fmt.Errorf("migrate sqlite: %w", err)
		}
	}

	s.db = db
	return nil
}

func (s *Store) Stop(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- s.db.Close() }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
