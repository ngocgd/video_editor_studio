package secrets

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	dbgen "loomtale/api/internal/db/gen"
)

// emptyDB answers every single-row query with pgx.ErrNoRows, like a
// secrets table that holds nothing for the tenant.
type emptyDB struct{ dbgen.DBTX }

func (emptyDB) QueryRow(context.Context, string, ...interface{}) pgx.Row { return noRow{} }

type noRow struct{}

func (noRow) Scan(...any) error { return pgx.ErrNoRows }

func TestOpenMissingSecretWrapsNotFoundAndNoRows(t *testing.T) {
	s := &Store{Queries: dbgen.New(emptyDB{})}
	_, err := s.Open(context.Background(), uuid.New(), KindYouTubeRefresh, "owner")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Open error %v does not wrap ErrNotFound", err)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("Open error %v does not wrap pgx.ErrNoRows", err)
	}
}

func TestGetMissingLLMKeyKeepsNoRowsContract(t *testing.T) {
	s := &Store{Queries: dbgen.New(emptyDB{})}
	// The LLM provider registry falls back to the operator's key only on
	// pgx.ErrNoRows, so Get must keep reporting a missing key that way.
	_, err := s.Get(context.Background(), uuid.New(), "anthropic-api")
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("Get error %v does not wrap pgx.ErrNoRows", err)
	}
}
