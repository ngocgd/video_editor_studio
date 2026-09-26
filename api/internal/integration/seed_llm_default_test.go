//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loomtale/api/internal/db/migrations"
)

const seedLLMDefaultFile = "20260927600000_seed_llm_default.sql"

// seedLLMDefaultSections returns the goose Up and Down sections of the
// seed migration as the loomtale CLI embeds it.
func seedLLMDefaultSections(t *testing.T) (up, down string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile(seedLLMDefaultFile)
	if err != nil {
		t.Fatal(err)
	}
	body, found := strings.CutPrefix(string(raw), "-- +goose Up")
	if !found {
		t.Fatalf("%s does not start with a goose Up section", seedLLMDefaultFile)
	}
	up, down, found = strings.Cut(body, "-- +goose Down")
	if !found {
		t.Fatalf("%s has no goose Down section", seedLLMDefaultFile)
	}
	return up, down
}

// TestSeedLLMDefault replays the seed migration inside a transaction that
// is rolled back, so the shared stack is left as it was. It checks that
// the seed waits for an installed local LLM, only switches tenants
// without a choice, is idempotent, and that the down migration restores
// only the tenants it switched and did not change since.
func TestSeedLLMDefault(t *testing.T) {
	ctx := context.Background()
	pool := ownerPool(t)
	up, down := seedLLMDefaultSections(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	exec := func(what, sql string, args ...any) {
		t.Helper()
		if len(args) == 0 {
			args = []any{pgx.QueryExecModeSimpleProtocol}
		}
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	provider := func(tenant uuid.UUID) string {
		t.Helper()
		var p string
		err := tx.QueryRow(ctx, "SELECT default_provider FROM llm_settings WHERE tenant_id = $1", tenant).Scan(&p)
		if errors.Is(err, pgx.ErrNoRows) {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Start from the pre-seed state with no local LLM installed.
	exec("down", down)
	exec("clear installs", "DELETE FROM model_installs WHERE name IN ('qwen3.5-9b', 'gemma-4-12b')")

	unset, chose, rechose := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{unset, chose, rechose} {
		exec("create tenant", "INSERT INTO tenants (id, name) VALUES ($1, $2)", id, "seed-llm-"+id.String())
	}
	exec("explicit choice",
		"INSERT INTO llm_settings (tenant_id, default_provider) VALUES ($1, 'anthropic-api')", chose)

	exec("up without a local LLM", up)
	if p := provider(unset); p != "" {
		t.Fatalf("without an installed local LLM the seed must not switch tenants, got %q", p)
	}

	exec("install a candidate", `INSERT INTO model_installs (name, status, licence_spdx, licence_url, revision, installed_at)
		VALUES ('qwen3.5-9b', 'installed', 'Apache-2.0', 'https://huggingface.co/Qwen/Qwen3.5-9B', 'test', now())`)
	exec("up", up)
	if p := provider(unset); p != "ollama" {
		t.Fatalf("a tenant without a choice must be seeded to ollama, got %q", p)
	}
	if p := provider(rechose); p != "ollama" {
		t.Fatalf("a tenant without a choice must be seeded to ollama, got %q", p)
	}
	if p := provider(chose); p != "anthropic-api" {
		t.Fatalf("an explicit choice must be kept, got %q", p)
	}

	exec("up again", up)
	var seeds int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM llm_default_seeds WHERE tenant_id::text = ANY($1)",
		[]string{unset.String(), chose.String(), rechose.String()}).Scan(&seeds); err != nil {
		t.Fatal(err)
	}
	if seeds != 2 {
		t.Fatalf("a re-run must not record new seeds, got %d seeded fixture tenants", seeds)
	}

	// The tenant saves its settings again after the seed.
	exec("re-choose", "UPDATE llm_settings SET updated_at = clock_timestamp() WHERE tenant_id = $1", rechose)

	exec("down", down)
	if p := provider(unset); p != "" {
		t.Fatalf("down must return a seeded tenant to the fallback default, got %q", p)
	}
	if p := provider(rechose); p != "ollama" {
		t.Fatalf("down must keep a choice saved after the seed, got %q", p)
	}
	if p := provider(chose); p != "anthropic-api" {
		t.Fatalf("down must keep an explicit choice, got %q", p)
	}
}
