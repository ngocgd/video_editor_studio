package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// passwordParams are the current argon2id parameters (RFC 9106 "moderate"
// profile, sized for a ~400ms login budget on the deploy target). Changing
// these only affects new hashes; VerifyPassword detects and signals an
// old-params hash so the caller can rehash on successful login.
var passwordParams = argon2Params{
	memoryKiB:   64 * 1024,
	iterations:  3,
	parallelism: 2,
	saltLen:     16,
	keyLen:      32,
}

type argon2Params struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
	saltLen     uint32
	keyLen      uint32
}

// HashPassword returns an argon2id PHC-format hash of password using the
// current passwordParams.
func HashPassword(password string) (string, error) {
	salt := make([]byte, passwordParams.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}
	return hashWith(password, salt, passwordParams), nil
}

func hashWith(password string, salt []byte, p argon2Params) string {
	key := argon2.IDKey([]byte(password), salt, p.iterations, p.memoryKiB, p.parallelism, p.keyLen)
	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		p.memoryKiB, p.iterations, p.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

// ErrMalformedHash is returned by VerifyPassword when the stored hash is
// not a well-formed argon2id PHC string.
var ErrMalformedHash = errors.New("auth: malformed password hash")

// VerifyPassword checks password against an argon2id PHC-format hash. It
// reports needsRehash=true when the hash was produced with parameters
// older than the current passwordParams, so the caller can re-hash and
// persist the new hash after a successful login.
func VerifyPassword(hash, password string) (ok bool, needsRehash bool, err error) {
	p, salt, key, err := parsePHC(hash)
	if err != nil {
		return false, false, err
	}
	candidate := argon2.IDKey([]byte(password), salt, p.iterations, p.memoryKiB, p.parallelism, uint32(len(key)))
	match := subtle.ConstantTimeCompare(candidate, key) == 1
	if !match {
		return false, false, nil
	}
	needsRehash = p.memoryKiB != passwordParams.memoryKiB ||
		p.iterations != passwordParams.iterations ||
		p.parallelism != passwordParams.parallelism ||
		uint32(len(key)) != passwordParams.keyLen
	return true, needsRehash, nil
}

func parsePHC(hash string) (argon2Params, []byte, []byte, error) {
	// $argon2id$v=19$m=...,t=...,p=...$salt$key
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argon2Params{}, nil, nil, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != 19 {
		return argon2Params{}, nil, nil, ErrMalformedHash
	}
	var p argon2Params
	var parallelism uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memoryKiB, &p.iterations, &parallelism); err != nil {
		return argon2Params{}, nil, nil, ErrMalformedHash
	}
	p.parallelism = uint8(parallelism)
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argon2Params{}, nil, nil, ErrMalformedHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argon2Params{}, nil, nil, ErrMalformedHash
	}
	return p, salt, key, nil
}
