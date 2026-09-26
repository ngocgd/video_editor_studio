// Package secrets is the write-through layer over the envelope-encrypted
// secrets table: it is the only place that ever seals a plaintext secret
// (a BYOK LLM API key, a YouTube refresh token) for storage, or opens one
// back to plaintext for server-side code that genuinely needs it (to call
// or revoke at Google, for instance). A plaintext secret is never returned
// to an HTTP client.
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

// KindYouTubeRefresh is the secrets.kind value for a connected YouTube
// channel's Google refresh token; secrets.owner_ref holds the
// youtube_channels row id.
const KindYouTubeRefresh = "youtube_refresh"

// PutLLMAPIKey seals plaintext under kind=llm_api_key, owner_ref=provider
// for tenantID and upserts it. The plaintext is never logged or returned;
// callers must not retain it after this call.
func (s *Store) PutLLMAPIKey(ctx context.Context, tenantID uuid.UUID, provider, plaintext string) error {
	return s.Put(ctx, tenantID, KindLLMAPIKey, provider, plaintext)
}

// Get opens and returns the plaintext LLM API key for tenantID+provider.
// It exists for a future per-tenant BYOK provider constructor (no HTTP
// handler calls it; they only ever call Configured).
func (s *Store) Get(ctx context.Context, tenantID uuid.UUID, provider string) (string, error) {
	return s.Open(ctx, tenantID, KindLLMAPIKey, provider)
}

// Put seals plaintext under kind+ownerRef for tenantID and upserts it.
// The tenant, kind and owner are bound into the ciphertext as AAD, so a
// row copied to another owner cannot be opened.
func (s *Store) Put(ctx context.Context, tenantID uuid.UUID, kind, ownerRef, plaintext string) error {
	aad := envelope.AAD(tenantID.String(), kind, ownerRef)
	sealed, err := s.Sealer.Seal(aad, []byte(plaintext))
	if err != nil {
		return fmt.Errorf("secrets: seal: %w", err)
	}
	err = s.Queries.UpsertSecret(ctx, dbgen.UpsertSecretParams{
		ID:         idconv.ToPg(idconv.NewV7()),
		TenantID:   idconv.ToPg(tenantID),
		Kind:       kind,
		OwnerRef:   ownerRef,
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

// ErrNotFound is returned by Open when no secret exists.
var ErrNotFound = errors.New("secrets: not found")

// Open returns the plaintext secret stored under kind+ownerRef, or an
// error wrapping both ErrNotFound and pgx.ErrNoRows when there is none:
// callers that predate ErrNotFound (the LLM provider registry falls back
// to the operator's key on pgx.ErrNoRows) keep working.
func (s *Store) Open(ctx context.Context, tenantID uuid.UUID, kind, ownerRef string) (string, error) {
	row, err := s.Queries.GetSecret(ctx, dbgen.GetSecretParams{
		TenantID: idconv.ToPg(tenantID),
		Kind:     kind,
		OwnerRef: ownerRef,
	})
	if isNoRows(err) {
		return "", fmt.Errorf("secrets: get %s: %w (%w)", kind, ErrNotFound, err)
	}
	if err != nil {
		return "", fmt.Errorf("secrets: get: %w", err)
	}
	aad := envelope.AAD(tenantID.String(), kind, ownerRef)
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

// Delete removes the secret stored under kind+ownerRef; deleting a
// missing secret is not an error.
func (s *Store) Delete(ctx context.Context, tenantID uuid.UUID, kind, ownerRef string) error {
	if err := s.Queries.DeleteSecret(ctx, dbgen.DeleteSecretParams{
		TenantID: idconv.ToPg(tenantID),
		Kind:     kind,
		OwnerRef: ownerRef,
	}); err != nil {
		return fmt.Errorf("secrets: delete: %w", err)
	}
	return nil
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
