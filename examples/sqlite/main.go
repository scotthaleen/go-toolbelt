package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/scotthaleen/go-app"
	"github.com/scotthaleen/go-toolbelt/logging"
	"github.com/scotthaleen/go-toolbelt/sqlite"
)

type Repository struct {
	store *sqlite.Store
}

func NewRepository() *Repository {
	return &Repository{}
}

func (r *Repository) Component() *app.Component {
	return app.NewComponent(
		app.WithName("repository"),
		app.WithOnStart(r.Start),
	)
}

func (r *Repository) Start(ctx context.Context) error {
	r.store = app.MustGet[*sqlite.Store](ctx)
	_, err := r.store.DB().ExecContext(ctx, `insert into notes (body) values (?)`, "hello from sqlite")
	if err != nil {
		return err
	}
	return nil
}

func main() {
	verbosity := 0
	for _, arg := range os.Args[1:] {
		if len(arg) > 1 && arg[0] == '-' {
			for _, r := range arg[1:] {
				if r == 'v' {
					verbosity++
				}
			}
		}
	}
	logger := logging.Setup(logging.Config{Verbosity: verbosity, AddSource: true})

	store := sqlite.New(sqlite.Config{
		Migrations: []string{
			`create table notes (id integer primary key, body text not null)`,
		},
	})
	repo := NewRepository()

	a := app.New(
		context.Background(),
		app.WithSignalHandling(false),
		app.WithLogger(logger),
		app.WithSequentialStartup(
			app.Registered(store),
			app.Registered(repo),
		),
	)

	if err := a.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	defer func() {
		slog.Debug("stopping app")
		if err := a.Close(context.Background()); err != nil {
			log.Printf("shutdown failed: %v", err)
		}
	}()

	rows, err := store.DB().QueryContext(context.Background(), `select id, body from notes order by id`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var body string
		if err := rows.Scan(&id, &body); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%d: %s\n", id, body)
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
}
