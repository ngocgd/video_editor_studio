package auth

import "testing"

func TestHashAndVerifyRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	ok, rehash, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected verification to succeed with the correct password")
	}
	if rehash {
		t.Fatal("freshly hashed password should not need a rehash")
	}
}

func TestVerifyRejectsWrongPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	ok, _, err := VerifyPassword(hash, "wrong password")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected verification to fail with the wrong password")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	for _, h := range []string{"", "not-a-phc-string", "$argon2id$v=19$m=x$salt$key", "$bcrypt$10$abc"} {
		if _, _, err := VerifyPassword(h, "anything"); err != ErrMalformedHash {
			t.Fatalf("hash %q: expected ErrMalformedHash, got %v", h, err)
		}
	}
}

func TestVerifyFlagsOutdatedParamsForRehash(t *testing.T) {
	old := argon2Params{memoryKiB: 8 * 1024, iterations: 1, parallelism: 1, saltLen: 16, keyLen: 32}
	salt := []byte("0123456789abcdef")
	hash := hashWith("a password", salt, old)

	ok, rehash, err := VerifyPassword(hash, "a password")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected verification to succeed against an old-params hash")
	}
	if !rehash {
		t.Fatal("expected needsRehash=true for a hash weaker than the current params")
	}
}

func TestHashesAreSalted(t *testing.T) {
	a, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("expected two hashes of the same password to differ (random salt)")
	}
}
