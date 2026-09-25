package main

import (
	"context"
	"database/sql"
	"flag"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"loomtale/api/internal/db/migrations"
)

const migrationsDir = "."

// runMigrate applies or rolls back goose migrations embedded from
// db/migrations, then River's own schema (river_job and friends), both
// against DATABASE_URL. This must run with the loomtale_owner role: the
// api/worker processes only ever hold the least-privilege loomtale_app
// role, which cannot CREATE TABLE. Usage: loomtale migrate up|down|status.
func runMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	direction := "up"
	if fs.NArg() > 0 {
		direction = fs.Arg(0)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errRequiredEnv("DATABASE_URL")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}

	switch direction {
	case "up":
		if err := goose.UpContext(ctx, db, migrationsDir); err != nil {
			return err
		}
		return riverMigrateUp(ctx, dsn)
	case "down":
		return goose.DownContext(ctx, db, migrationsDir)
	case "status":
		return goose.StatusContext(ctx, db, migrationsDir)
	default:
		return errUnknownDirection(direction)
	}
}

// riverMigrateUp applies River's own migrations (idempotent: each is
// applied at most once). It uses a separate pgx/v5 pool because
// rivermigrate's driver needs pgxpool.Pool, not database/sql.
func riverMigrateUp(ctx context.Context, dsn string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return err
	}
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	return err
}

type errRequiredEnv string

func (e errRequiredEnv) Error() string { return "required environment variable not set: " + string(e) }

type errUnknownDirection string

func (e errUnknownDirection) Error() string { return "unknown migrate direction: " + string(e) }
