package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
	"time"
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

	db := store.DB()
	if err := store.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if store.DB() != nil {
		t.Fatal("DB() is non-nil after Stop")
	}
	if err := db.PingContext(context.Background()); err == nil {
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

func TestMigrationListIsAtomic(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "atomic.db")
	store := New(Config{
		DSN: dsn,
		Migrations: []string{
			`create table notes (body text not null)`,
			`create table broken (`,
		},
	})
	if err := store.Start(context.Background()); err == nil {
		t.Fatal("Start() error = nil, want migration error")
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`select count(*) from sqlite_master where type = 'table' and name = 'notes'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("first migration remained committed after later failure")
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
	store := New(Config{
		DSN:          "file:pool-settings?mode=memory&cache=shared",
		MaxOpenConns: 3,
		MaxIdleConns: 2,
	})
	if err := store.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Stop(context.Background()) })

	stats := store.DB().Stats()
	if stats.MaxOpenConnections != 3 {
		t.Fatalf("MaxOpenConnections = %d, want 3", stats.MaxOpenConnections)
	}
}

func TestMemoryDatabaseRejectsUnsafePoolSettings(t *testing.T) {
	for _, cfg := range []Config{
		{MaxOpenConns: 2},
		{MaxIdleTime: time.Second},
	} {
		store := New(cfg)
		if err := store.Start(context.Background()); err == nil {
			t.Fatalf("Start(%+v) error = nil, want unsafe :memory: configuration error", cfg)
		}
	}
}

func TestStartJoinsMigratorAndCleanupErrors(t *testing.T) {
	migrateErr := errors.New("migration failed")
	closeErr := errors.New("close failed")
	db := openTestDB(t, closeErr)
	store := New(Config{Migrate: func(context.Context, *sql.DB) error { return migrateErr }})
	store.open = func(string, string) (*sql.DB, error) { return db, nil }

	err := store.Start(context.Background())
	if !errors.Is(err, migrateErr) || !errors.Is(err, closeErr) {
		t.Fatalf("Start() error = %v, want migration and close errors", err)
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

func TestStartRejectsAlreadyStartedStore(t *testing.T) {
	store := New(Config{})
	if err := store.Start(context.Background()); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Stop(context.Background()) })
	db := store.DB()
	if err := store.Start(context.Background()); err == nil {
		t.Fatal("second Start() error = nil, want already started error")
	}
	if store.DB() != db {
		t.Fatal("second Start() replaced the database")
	}
}

func TestStopWithCanceledContextStillClosesDB(t *testing.T) {
	store := New(Config{})
	if err := store.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	db := store.DB()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() error = %v, want context.Canceled", err)
	}
	if store.DB() != nil {
		t.Fatal("DB() is non-nil after Stop")
	}
	if err := store.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if err := db.PingContext(context.Background()); err == nil {
		t.Fatal("database remains open after Stop")
	}
}

func TestStopRespectsContextDeadline(t *testing.T) {
	release := make(chan struct{})
	store := New(Config{})
	store.db = openBlockingTestDB(t, release)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := store.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want context.DeadlineExceeded", err)
	}
	if store.DB() != nil {
		t.Fatal("DB() is non-nil while close finishes")
	}
	close(release)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := store.Stop(waitCtx); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestStartReportsCompletedCloseError(t *testing.T) {
	wantErr := errors.New("close failed")
	release := make(chan struct{})
	store := New(Config{})
	store.db = sql.OpenDB(testConnector{closeErr: wantErr, closeWait: release})
	if err := store.db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() error = %v, want context.Canceled", err)
	}
	closing := store.closing
	close(release)
	<-closing.done

	if err := store.Start(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Start() error = %v, want errors.Is(_, %v)", err, wantErr)
	}
}

func TestStopClearsDBOnCloseError(t *testing.T) {
	wantErr := errors.New("close failed")
	store := New(Config{})
	store.db = openTestDB(t, wantErr)

	if err := store.Stop(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Stop() error = %v, want errors.Is(_, %v)", err, wantErr)
	}
	if store.DB() != nil {
		t.Fatal("DB() is non-nil after Stop")
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

func openTestDB(t *testing.T, closeErr error) *sql.DB {
	t.Helper()
	db := sql.OpenDB(testConnector{closeErr: closeErr})
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	return db
}

func openBlockingTestDB(t *testing.T, closeWait <-chan struct{}) *sql.DB {
	t.Helper()
	db := sql.OpenDB(testConnector{closeWait: closeWait})
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	return db
}

type testConnector struct {
	closeErr  error
	closeWait <-chan struct{}
}

func (c testConnector) Connect(context.Context) (driver.Conn, error) {
	return testConn{closeErr: c.closeErr, closeWait: c.closeWait}, nil
}

func (c testConnector) Driver() driver.Driver {
	return testDriver{closeErr: c.closeErr, closeWait: c.closeWait}
}

type testDriver struct {
	closeErr  error
	closeWait <-chan struct{}
}

func (d testDriver) Open(string) (driver.Conn, error) {
	return testConn{closeErr: d.closeErr, closeWait: d.closeWait}, nil
}

type testConn struct {
	closeErr  error
	closeWait <-chan struct{}
}

func (c testConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not implemented") }
func (c testConn) Close() error {
	if c.closeWait != nil {
		<-c.closeWait
	}
	return c.closeErr
}
func (c testConn) Begin() (driver.Tx, error)  { return nil, errors.New("not implemented") }
func (c testConn) Ping(context.Context) error { return nil }
