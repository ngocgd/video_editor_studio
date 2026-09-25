package envelope

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// DefaultKEKPath is where compose mounts the master_key secret.
const DefaultKEKPath = "/run/secrets/master_key"

// LoadKEK reads a base64-encoded 32-byte AES-256 key from path. It fails
// fast (non-nil error) if the file is absent, unreadable, not valid
// base64, or not exactly 32 bytes after decoding, so the process never
// starts in a state where secrets could silently go unencrypted.
func LoadKEK(path string) (key [32]byte, keyID string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return key, "", fmt.Errorf("envelope: read KEK file %q: %w", path, err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return key, "", fmt.Errorf("envelope: KEK file %q is not valid base64: %w", path, err)
	}
	if len(decoded) != 32 {
		return key, "", fmt.Errorf("envelope: KEK file %q decodes to %d bytes, want 32", path, len(decoded))
	}
	copy(key[:], decoded)
	// key_id is a non-reversible fingerprint of the key, used only as a
	// rotation label to pick which KEK a stored secret was sealed under;
	// it never lets anyone recover the key itself.
	sum := sha256.Sum256(decoded)
	keyID = "kek-" + hex.EncodeToString(sum[:8])
	return key, keyID, nil
}
