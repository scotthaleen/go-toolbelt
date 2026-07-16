package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/scotthaleen/go-app"

	_ "github.com/jackc/pgx/v5/stdlib"
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
	return &Store{cfg: cfg}
}

func (s *Store) Component() *app.Component {
	return app.NewComponent(
		app.WithName("postgres"),
		app.WithOnStart(s.Start),
		app.WithOnStop(s.Stop),
	)
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Start(ctx context.Context) error {
	if s.cfg.DSN == "" {
		return errors.New("postgres dsn is required")
	}

	db, err := sql.Open("pgx", s.cfg.DSN)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
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
		return fmt.Errorf("ping postgres: %w", err)
	}

	for _, migration := range s.cfg.Migrations {
		if _, err := db.ExecContext(ctx, migration); err != nil {
			_ = db.Close()
			return fmt.Errorf("run postgres migration: %w", err)
		}
	}
	if s.cfg.Migrate != nil {
		if err := s.cfg.Migrate(ctx, db); err != nil {
			_ = db.Close()
			return fmt.Errorf("migrate postgres: %w", err)
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
