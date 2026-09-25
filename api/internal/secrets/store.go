// Package secrets is the write-through layer over the envelope-encrypted
// secrets table: it is the only place that ever seals a plaintext secret
// (e.g. a BYOK LLM API key) for storage, or opens one back to plaintext
// for a caller that genuinely needs it (never an HTTP handler — see
// Configured, which is the only method exposed to settingsapi).
package secrets

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loomtale/api/internal/crypto/envelope"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// Store persists and checks secrets via envelope encryption. Sealer holds
// the process's single active KEK (see envelope.LoadKEK); Queries is a
// tenant-scoped *dbgen.Queries.
type Store struct {
	Sealer  *envelope.Sealer
	Queries *dbgen.Queries
}

// Configured reports whether a secret of kind="llm_api_key" exists for
// tenantID+provider, without ever decrypting or returning it. Implements
// settingsapi.SecretsChecker.
func (s *Store) Configured(ctx context.Context, tenantID, provider string) (bool, error) {
	id, err := uuid.Parse(tenantID)
	if err != nil {
		return false, fmt.Errorf("secrets: invalid tenant id: %w", err)
	}
	_, err = s.Queries.GetSecret(ctx, dbgen.GetSecretParams{
		TenantID: idconv.ToPg(id),
		Kind:     KindLLMAPIKey,
		OwnerRef: provider,
	})
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, fmt.Errorf("secrets: check configured: %w", err)
	}
	return true, nil
}

// KindLLMAPIKey is the secrets.kind value for a BYOK LLM provider API
// key; secrets.owner_ref holds the provider name.
const KindLLMAPIKey = "llm_api_key"

// PutLLMAPIKey seals plaintext under kind=llm_api_key, owner_ref=provider
// for tenantID and upserts it. The plaintext is never logged or returned;
// callers must not retain it after this call.
func (s *Store) PutLLMAPIKey(ctx context.Context, tenantID uuid.UUID, provider, plaintext string) error {
	aad := envelope.AAD(tenantID.String(), KindLLMAPIKey, provider)
	sealed, err := s.Sealer.Seal(aad, []byte(plaintext))
	if err != nil {
		return fmt.Errorf("secrets: seal: %w", err)
	}
	err = s.Queries.UpsertSecret(ctx, dbgen.UpsertSecretParams{
		ID:         idconv.ToPg(idconv.NewV7()),
		TenantID:   idconv.ToPg(tenantID),
		Kind:       KindLLMAPIKey,
		OwnerRef:   provider,
		KeyID:      sealed.KeyID,
		WrappedDek: sealed.WrappedDEK,
		Nonce:      sealed.Nonce,
		Ciphertext: sealed.Ciphertext,
	})
	if err != nil {
		return fmt.Errorf("secrets: upsert: %w", err)
	}
	return nil
}

// Get opens and returns the plaintext secret for tenantID+provider. It
// exists for a future per-tenant BYOK provider constructor (not called by
// any phase 6 HTTP handler, which only ever calls Configured); kept here
// rather than duplicated so the seal/open pair stays in one place.
func (s *Store) Get(ctx context.Context, tenantID uuid.UUID, provider string) (string, error) {
	row, err := s.Queries.GetSecret(ctx, dbgen.GetSecretParams{
		TenantID: idconv.ToPg(tenantID),
		Kind:     KindLLMAPIKey,
		OwnerRef: provider,
	})
	if err != nil {
		return "", fmt.Errorf("secrets: get: %w", err)
	}
	aad := envelope.AAD(tenantID.String(), KindLLMAPIKey, provider)
	plaintext, err := s.Sealer.Open(envelope.Sealed{
		KeyID:      row.KeyID,
		WrappedDEK: row.WrappedDek,
		Nonce:      row.Nonce,
		Ciphertext: row.Ciphertext,
	}, aad)
	if err != nil {
		return "", fmt.Errorf("secrets: open: %w", err)
	}
	return string(plaintext), nil
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
