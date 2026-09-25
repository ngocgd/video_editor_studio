//go:build integration

// Package integration exercises phase 2 (auth, RBAC, tenant isolation,
// presign/finalize, audit immutability) against a live stack: the API
// over HTTP (API_BASE_URL, default http://127.0.0.1:8080/api/v1) and
// Postgres directly (OWNER_DATABASE_URL / DATABASE_URL) for fixtures and
// role-permission assertions the HTTP surface cannot reach.
//
// Run via `docker compose -p loomtale-p2 -f deploy/compose.yml up -d --wait`
// then `make test-integration` (or `go test -tags=integration ./internal/integration/...`
// from a toolbox container attached to that compose project's network).
package integration

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

func baseURL() string {
	if v := os.Getenv("API_BASE_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:8080/api/v1"
}

// inCI reports whether this run must treat a missing prerequisite as a
// hard failure instead of a skip: CI is set by GitHub Actions (and most
// other CI systems) automatically, so a misconfigured integration job
// that forgot to provision the stack fails loudly instead of silently
// reporting all-green with zero tests actually exercised.
func inCI() bool {
	return os.Getenv("CI") != ""
}

func missingEnv(t *testing.T, name, reason string) {
	t.Helper()
	msg := name + " not set; " + reason
	if inCI() {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

func ownerPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("OWNER_DATABASE_URL")
	if dsn == "" {
		missingEnv(t, "OWNER_DATABASE_URL", "needs the loomtale_owner DSN of a running stack")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect as owner: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func appPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		missingEnv(t, "DATABASE_URL", "needs the loomtale_app DSN of a running stack")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect as app: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func backupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("BACKUP_DATABASE_URL")
	if dsn == "" {
		missingEnv(t, "BACKUP_DATABASE_URL", "needs the loomtale_backup DSN of a running stack")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect as backup: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// fixtureUser is a tenant + user + membership created directly through
// the owner DB connection (mirroring what `loomtale create-owner` does),
// used to seed test fixtures without depending on the CLI binary.
type fixtureUser struct {
	Email      string
	Password   string
	TenantID   uuid.UUID
	TenantName string
	UserID     uuid.UUID
}

// uniqueEmail returns a fresh email for prefix so parallel test runs never
// collide on the users.email uniqueness constraint.
func uniqueEmail(prefix string) string {
	return prefix + "-" + uuid.NewString() + "@integration.test"
}

func createFixtureUser(t *testing.T, q *gen.Queries, tenantName, email, role string) fixtureUser {
	t.Helper()
	ctx := context.Background()
	password := "Sup3rSecret!" + uuid.NewString()[:8]

	tenant, err := q.CreateTenant(ctx, gen.CreateTenantParams{ID: idconv.ToPg(idconv.NewV7()), Name: tenantName})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	hash, err := authpkg.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := q.CreateUser(ctx, gen.CreateUserParams{ID: idconv.ToPg(idconv.NewV7()), Email: email, PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := q.CreateMembership(ctx, gen.CreateMembershipParams{TenantID: tenant.ID, UserID: user.ID, Role: role}); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	return fixtureUser{
		Email:      email,
		Password:   password,
		TenantID:   idconv.FromPg(tenant.ID),
		TenantName: tenantName,
		UserID:     idconv.FromPg(user.ID),
	}
}
