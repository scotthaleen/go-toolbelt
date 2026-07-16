package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestStartRunsMigrationsAndStopClosesDB(t *testing.T) {
	store := New(Config{
		DSN: "file:sqlite-test?mode=memory&cache=shared",
		Migrations: []string{
			`create table notes (id integer primary key, body text not null)`,
			`insert into notes (body) values ('hello')`,
		},
	})

	if err := store.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if store.DB() == nil {
		t.Fatal("DB() = nil after Start")
	}

	var count int
	if err := store.DB().QueryRowContext(context.Background(), `select count(*) from notes`).Scan(&count); err != nil {
		t.Fatalf("query migrated table: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	if err := store.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := store.DB().PingContext(context.Background()); err == nil {
		t.Fatal("PingContext() error = nil after Stop, want closed DB")
	}
}

func TestStartClosesDBOnMigrationError(t *testing.T) {
	store := New(Config{Migrations: []string{`create table broken (`}})
	if err := store.Start(context.Background()); err == nil {
		t.Fatal("Start() error = nil, want migration error")
	}
	if store.DB() != nil {
		t.Fatal("DB() is non-nil after failed start")
	}
}

func TestStartRunsMigrator(t *testing.T) {
	var migratedDB *sql.DB
	calls := 0
	store := New(Config{
		Migrate: func(ctx context.Context, db *sql.DB) error {
			calls++
			migratedDB = db
			_, err := db.ExecContext(ctx, `create table notes (body text not null)`)
			return err
		},
	})

	if err := store.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Stop(context.Background()) })
	if calls != 1 {
		t.Fatalf("migrator calls = %d, want 1", calls)
	}
	if migratedDB == nil || migratedDB != store.DB() {
		t.Fatal("migrator did not receive the store database")
	}
	if err := store.DB().QueryRow(`select count(*) from notes`).Scan(new(int)); err != nil {
		t.Fatalf("query migrated table: %v", err)
	}
}

func TestStartClosesDBOnMigratorError(t *testing.T) {
	wantErr := errors.New("migration failed")
	var migratedDB *sql.DB
	store := New(Config{
		Migrate: func(_ context.Context, db *sql.DB) error {
			migratedDB = db
			return wantErr
		},
	})

	err := store.Start(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Start() error = %v, want errors.Is(_, %v)", err, wantErr)
	}
	if store.DB() != nil {
		t.Fatal("DB() is non-nil after failed start")
	}
	if migratedDB == nil {
		t.Fatal("migrator did not receive a database")
	}
	if err := migratedDB.PingContext(context.Background()); err == nil {
		t.Fatal("migrator database remains open after failed start")
	}
	if err := store.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() after failed start error = %v", err)
	}
}

func TestPoolSettings(t *testing.T) {
	store := New(Config{MaxOpenConns: 3, MaxIdleConns: 2})
	if err := store.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Stop(context.Background()) })

	stats := store.DB().Stats()
	if stats.MaxOpenConnections != 3 {
		t.Fatalf("MaxOpenConnections = %d, want 3", stats.MaxOpenConnections)
	}
}

func TestStopBeforeStart(t *testing.T) {
	store := New(Config{})
	if err := store.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestStopIsRepeatable(t *testing.T) {
	store := New(Config{})
	if err := store.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for i := range 2 {
		if err := store.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() call %d error = %v", i+1, err)
		}
	}
}

func TestDefaultStoresUsePrivateDatabases(t *testing.T) {
	first := New(Config{Migrations: []string{`create table notes (body text not null)`}})
	second := New(Config{})
	for _, store := range []*Store{first, second} {
		if err := store.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Stop(context.Background()) })
	}

	if err := second.DB().QueryRow(`select count(*) from notes`).Scan(new(int)); err == nil {
		t.Fatal("second default store can see first store's schema")
	}
}
