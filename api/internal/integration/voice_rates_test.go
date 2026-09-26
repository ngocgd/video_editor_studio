//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loomtale/api/internal/db/gen"
	"loomtale/api/internal/speechrate"
)

// TestVoiceRateCalibrationRoundTrip stores a measured rate as the app
// role and reads it back the way the duration estimate will.
func TestVoiceRateCalibrationRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := appPool(t)
	store := &speechrate.Store{Queries: gen.New(pool)}
	key := "test-engine:voice:" + uuid.NewString()
	owner := ownerPool(t)
	t.Cleanup(func() {
		_, _ = owner.Exec(context.Background(), "DELETE FROM voice_rate_calibrations WHERE voice_key = $1", key)
	})

	if _, ok, err := store.Lookup(ctx, key); err != nil || ok {
		t.Fatalf("an uncalibrated voice must report ok=false, got %v %v", ok, err)
	}
	cal, err := speechrate.Calibrate(key, "test-engine", "vi", []speechrate.Sample{{Words: 330, Seconds: 120}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, cal, uuid.New()); err != nil {
		t.Fatal(err)
	}
	cal.WPM = 170
	if err := store.Save(ctx, cal, uuid.New()); err != nil {
		t.Fatalf("a re-run must overwrite the calibration: %v", err)
	}
	wpm, ok, err := store.Lookup(ctx, key)
	if err != nil || !ok || wpm != 170 {
		t.Fatalf("lookup = %v %v %v", wpm, ok, err)
	}
}

// TestModelFileDigestsAcceptSha256AndGitBlobPins checks the digest
// constraint that lets small config files be pinned by git blob id.
func TestModelFileDigestsAcceptSha256AndGitBlobPins(t *testing.T) {
	ctx := context.Background()
	q := gen.New(appPool(t))
	prefix := "integration-test/" + uuid.NewString()
	owner := ownerPool(t)
	t.Cleanup(func() {
		_, _ = owner.Exec(context.Background(), "DELETE FROM model_files WHERE path LIKE $1", prefix+"%")
	})
	for _, digest := range []string{strings.Repeat("a", 64), "git-sha1:" + strings.Repeat("b", 40)} {
		if err := q.UpsertModelFile(ctx, gen.UpsertModelFileParams{Path: prefix + "/" + digest[:4], Digest: digest, SizeBytes: 1}); err != nil {
			t.Fatalf("digest %s refused: %v", digest, err)
		}
	}
	for _, bad := range []string{"sha256:" + strings.Repeat("a", 64), "git-sha1:xyz", strings.Repeat("A", 64)} {
		if err := q.UpsertModelFile(ctx, gen.UpsertModelFileParams{Path: prefix + "/bad", Digest: bad, SizeBytes: 1}); err == nil {
			t.Fatalf("digest %q must be refused", bad)
		}
	}
}
