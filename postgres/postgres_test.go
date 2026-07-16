package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStartRequiresDSN(t *testing.T) {
	store := New(Config{})
	err := store.Start(context.Background())
	if err == nil {
		t.Fatal("Start() error = nil, want DSN error")
	}
	if !strings.Contains(err.Error(), "dsn is required") {
		t.Fatalf("Start() error = %v, want DSN error", err)
	}
	if store.DB() != nil {
		t.Fatal("DB() is non-nil after failed start")
	}
}

func TestStartRejectsAlreadyStartedStore(t *testing.T) {
	store := New(Config{DSN: "unused"})
	store.db = openTestDB(t, nil)
	t.Cleanup(func() { _ = store.Stop(context.Background()) })
	db := store.DB()

	if err := store.Start(context.Background()); err == nil {
		t.Fatal("Start() error = nil, want already started error")
	}
	if store.DB() != db {
		t.Fatal("second Start() replaced the database")
	}
}

func TestStopClearsDBOnCloseError(t *testing.T) {
	wantErr := errors.New("close failed")
	store := New(Config{DSN: "unused"})
	store.db = openTestDB(t, wantErr)

	if err := store.Stop(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Stop() error = %v, want errors.Is(_, %v)", err, wantErr)
	}
	if store.DB() != nil {
		t.Fatal("DB() is non-nil after Stop")
	}
	if err := store.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestStopRespectsContextDeadline(t *testing.T) {
	release := make(chan struct{})
	store := New(Config{DSN: "unused"})
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
	store := New(Config{DSN: "unused"})
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

func TestStartRunsMigrator(t *testing.T) {
	db := openTestDB(t, nil)
	calls := 0
	store := New(Config{
		DSN: "unused",
		Migrate: func(_ context.Context, got *sql.DB) error {
			calls++
			if got != db {
				t.Fatal("migrator received unexpected database")
			}
			return nil
		},
	})
	store.open = func(string, string) (*sql.DB, error) { return db, nil }

	if err := store.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Stop(context.Background()) })
	if calls != 1 {
		t.Fatalf("migrator calls = %d, want 1", calls)
	}
}

func TestStartClosesDBOnMigratorError(t *testing.T) {
	wantErr := errors.New("migration failed")
	db := openTestDB(t, nil)
	store := New(Config{
		DSN:     "unused",
		Migrate: func(context.Context, *sql.DB) error { return wantErr },
	})
	store.open = func(string, string) (*sql.DB, error) { return db, nil }

	if err := store.Start(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Start() error = %v, want errors.Is(_, %v)", err, wantErr)
	}
	if store.DB() != nil {
		t.Fatal("DB() is non-nil after failed start")
	}
	if err := db.PingContext(context.Background()); err == nil {
		t.Fatal("database remains open after failed start")
	}
}

func TestStartJoinsMigratorAndCleanupErrors(t *testing.T) {
	migrateErr := errors.New("migration failed")
	closeErr := errors.New("close failed")
	db := openTestDB(t, closeErr)
	store := New(Config{
		DSN:     "unused",
		Migrate: func(context.Context, *sql.DB) error { return migrateErr },
	})
	store.open = func(string, string) (*sql.DB, error) { return db, nil }

	err := store.Start(context.Background())
	if !errors.Is(err, migrateErr) || !errors.Is(err, closeErr) {
		t.Fatalf("Start() error = %v, want migration and close errors", err)
	}
}

func TestStopWithCanceledContextStillClosesDB(t *testing.T) {
	store := New(Config{DSN: "unused"})
	store.db = openTestDB(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() error = %v, want context.Canceled", err)
	}
	if store.DB() != nil {
		t.Fatal("DB() is non-nil after Stop")
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
