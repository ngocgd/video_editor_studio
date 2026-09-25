package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"

	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// runCreateOwner provisions a fresh tenant with a single owner user,
// replacing open signup. Usage:
//
//	loomtale create-owner -tenant "Acme" -email owner@example.com
//
// The password is always read from stdin (a TTY prompt, or piped input in
// automation), never from argv, so it never lands in shell history or a
// process listing.
func runCreateOwner(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("create-owner", flag.ExitOnError)
	tenantName := fs.String("tenant", "", "tenant display name (required)")
	email := fs.String("email", "", "owner email (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tenantName == "" || *email == "" {
		return errors.New("usage: loomtale create-owner -tenant <name> -email <email>")
	}

	password, err := readPassword()
	if err != nil {
		return err
	}
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errRequiredEnv("DATABASE_URL")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := gen.New(pool)

	if _, err := q.GetUserByEmail(ctx, strings.ToLower(*email)); err == nil {
		return fmt.Errorf("a user with email %q already exists", *email)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	hash, err := authpkg.HashPassword(password)
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := q.WithTx(tx)

	tenant, err := qtx.CreateTenant(ctx, gen.CreateTenantParams{ID: idconv.ToPg(idconv.NewV7()), Name: *tenantName})
	if err != nil {
		return err
	}
	user, err := qtx.CreateUser(ctx, gen.CreateUserParams{ID: idconv.ToPg(idconv.NewV7()), Email: *email, PasswordHash: hash})
	if err != nil {
		return err
	}
	if _, err := qtx.CreateMembership(ctx, gen.CreateMembershipParams{TenantID: tenant.ID, UserID: user.ID, Role: "owner"}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	fmt.Printf("created tenant %q (%s) with owner %s\n", *tenantName, idconv.FromPg(tenant.ID), *email)
	return nil
}

// readPassword reads the password from stdin: a hidden TTY prompt when
// stdin is a terminal, or a single line when it is piped (CI/automation).
func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "password: ")
		raw, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
