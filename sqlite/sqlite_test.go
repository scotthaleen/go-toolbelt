package sqlite

import (
	"context"
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
