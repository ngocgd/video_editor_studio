// Package envelope implements envelope encryption for the secrets table:
// AES-256-GCM with a random per-record data-encryption key (DEK) wrapped
// by a key-encryption key (KEK) loaded from a mounted file, and AAD binding
// each ciphertext to the tenant, secret kind and record it belongs to so a
// row can never be decrypted after being copied to a different context.
package envelope

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// dekSize is 32 bytes: AES-256.
const dekSize = 32

// Sealed is the ciphertext and metadata stored in the secrets table.
type Sealed struct {
	KeyID      string
	WrappedDEK []byte
	Nonce      []byte
	Ciphertext []byte
}

// AAD deterministically builds the additional authenticated data binding a
// ciphertext to the record it belongs to, so swapping a ciphertext between
// records (even within the same tenant) fails to decrypt.
//
// ownerRef must be the row's natural key (secrets.owner_ref: "what this
// secret is for", e.g. a provider name or a YouTube channel id), never its
// surrogate id column. db/queries/secrets.sql's UpsertSecret is an
// INSERT ... ON CONFLICT (tenant_id, kind, owner_ref) that keeps the
// existing row's id on a conflict; binding AAD to the id would mean a
// caller that generates a fresh id for every Seal (the normal pattern
// elsewhere in this codebase) gets a ciphertext whose AAD no longer
// matches the row it lands in, and Open then fails permanently. owner_ref
// is stable across that upsert by construction.
func AAD(tenantID, kind, ownerRef string) []byte {
	return []byte(tenantID + "|" + kind + "|" + ownerRef)
}

// Sealer seals and opens secrets using a single KEK. Rotation to a new KEK
// is done by loading the new key under a new key_id and re-sealing
// existing rows; Open dispatches on the stored key_id so old rows stay
// readable during a rotation window if both keys are kept available (not
// implemented in this phase: only one active KEK is loaded at a time).
type Sealer struct {
	keyID string
	kek   cipher.AEAD
}

// NewSealer builds a Sealer from a raw 32-byte KEK and the key_id to stamp
// on every secret it seals.
func NewSealer(keyID string, kek [32]byte) (*Sealer, error) {
	block, err := aes.NewCipher(kek[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{keyID: keyID, kek: gcm}, nil
}

// Seal generates a fresh DEK, encrypts plaintext under it with aad, wraps
// the DEK with the KEK, and returns everything needed to store the secret.
func (s *Sealer) Seal(aad, plaintext []byte) (Sealed, error) {
	dek := make([]byte, dekSize)
	if _, err := rand.Read(dek); err != nil {
		return Sealed{}, fmt.Errorf("envelope: generate dek: %w", err)
	}
	defer zero(dek)

	dekGCM, err := newGCM(dek)
	if err != nil {
		return Sealed{}, err
	}

	dataNonce := make([]byte, dekGCM.NonceSize())
	if _, err := rand.Read(dataNonce); err != nil {
		return Sealed{}, fmt.Errorf("envelope: generate nonce: %w", err)
	}
	ciphertext := dekGCM.Seal(nil, dataNonce, plaintext, aad)

	wrapNonce := make([]byte, s.kek.NonceSize())
	if _, err := rand.Read(wrapNonce); err != nil {
		return Sealed{}, fmt.Errorf("envelope: generate wrap nonce: %w", err)
	}
	wrappedDEK := s.kek.Seal(wrapNonce, wrapNonce, dek, nil)

	return Sealed{
		KeyID:      s.keyID,
		WrappedDEK: wrappedDEK,
		Nonce:      dataNonce,
		Ciphertext: ciphertext,
	}, nil
}

// ErrKeyIDMismatch is returned by Open when sealed.KeyID does not match
// this Sealer's active key (rotation to a KEK this process was not given).
var ErrKeyIDMismatch = errors.New("envelope: sealed record uses a different key_id than the active KEK")

// Open reverses Seal: unwraps the DEK with the KEK, then decrypts and
// authenticates the ciphertext with aad. Any tampering with the
// ciphertext, wrapped DEK, or a mismatched aad causes decryption to fail.
func (s *Sealer) Open(sealed Sealed, aad []byte) ([]byte, error) {
	if sealed.KeyID != s.keyID {
		return nil, ErrKeyIDMismatch
	}
	if len(sealed.WrappedDEK) < s.kek.NonceSize() {
		return nil, errors.New("envelope: wrapped dek too short")
	}
	wrapNonce := sealed.WrappedDEK[:s.kek.NonceSize()]
	wrapCiphertext := sealed.WrappedDEK[s.kek.NonceSize():]
	dek, err := s.kek.Open(nil, wrapNonce, wrapCiphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("envelope: unwrap dek: %w", err)
	}
	defer zero(dek)

	dekGCM, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	// cipher.AEAD.Open panics (not errors) if the nonce is the wrong
	// length, so a corrupt or truncated row must be rejected here first
	// rather than letting a malformed database value crash the process.
	if len(sealed.Nonce) != dekGCM.NonceSize() {
		return nil, fmt.Errorf("envelope: nonce is %d bytes, want %d", len(sealed.Nonce), dekGCM.NonceSize())
	}
	plaintext, err := dekGCM.Open(nil, sealed.Nonce, sealed.Ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("envelope: decrypt: %w", err)
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
