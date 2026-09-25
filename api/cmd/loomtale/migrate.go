package main

import (
	"context"
	"database/sql"
	"flag"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"loomtale/api/internal/db/migrations"
)

const migrationsDir = "."

// runMigrate applies or rolls back goose migrations embedded from
// db/migrations against DATABASE_URL. Usage: loomtale migrate up|down|status.
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
		return goose.UpContext(ctx, db, migrationsDir)
	case "down":
		return goose.DownContext(ctx, db, migrationsDir)
	case "status":
		return goose.StatusContext(ctx, db, migrationsDir)
	default:
		return errUnknownDirection(direction)
	}
}

type errRequiredEnv string

func (e errRequiredEnv) Error() string { return "required environment variable not set: " + string(e) }

type errUnknownDirection string

func (e errUnknownDirection) Error() string { return "unknown migrate direction: " + string(e) }
