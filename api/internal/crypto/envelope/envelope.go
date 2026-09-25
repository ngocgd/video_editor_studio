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
func AAD(tenantID, kind, recordID string) []byte {
	return []byte(tenantID + "|" + kind + "|" + recordID)
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
