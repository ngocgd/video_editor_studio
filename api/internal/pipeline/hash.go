package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// HashInputs returns a stable content hash of parts, used by a
// StepHandler's InputHash to detect when a done step's output no longer
// reflects its current inputs. Map values are marshalled with sorted keys
// (encoding/json already sorts map[string]any keys) so semantically
// identical inputs always hash the same regardless of Go map iteration
// order or field ordering upstream.
func HashInputs(parts ...any) (string, error) {
	canonical := make([]json.RawMessage, len(parts))
	for i, p := range parts {
		b, err := json.Marshal(canonicalize(p))
		if err != nil {
			return "", err
		}
		canonical[i] = b
	}
	joined, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(joined)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalize round-trips v through JSON so map keys and nested
// structures are normalised the same way regardless of the concrete Go
// type passed in (struct vs. map[string]any produce the same hash for the
// same logical content).
func canonicalize(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return sortValue(out)
}

// sortValue recursively sorts map keys are already stable under
// encoding/json (Go's encoder sorts map[string]any keys), so this only
// needs to walk slices, which JSON preserves in order already; it exists
// to make the normalisation intent explicit and to leave a seam for a
// future case (e.g. sets) that need real reordering.
func sortValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			out[k] = sortValue(t[k])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = sortValue(e)
		}
		return out
	default:
		return t
	}
}
