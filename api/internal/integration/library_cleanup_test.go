//go:build integration

// Library tests: listing and usage, a dry-run cleanup whose token is
// refused once the candidate set changed, the confirmed cleanup run by
// the live worker (rows, every object version and the audit entry), and
// the daily TTL cleanup scheduled for a due tenant.
package integration

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/library"
)

type cleanupPreviewDTO struct {
	Segments []struct {
		ID string `json:"id"`
	} `json:"segments"`
	Takes []struct {
		ID      string `json:"id"`
		AssetID string `json:"assetId"`
	} `json:"takes"`
	Bytes     int64  `json:"bytes"`
	Truncated bool   `json:"truncated"`
	Token     string `json:"token"`
}

// imageTakes returns a scene's image take ids, oldest first, and the
// selected one.
func imageTakes(t *testing.T, f renderFixture, sceneID string) (ids []string, selected string) {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT id, selected FROM scene_takes WHERE scene_id = $1 AND kind = 'image' ORDER BY created_at, id`, sceneID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var sel bool
		if err := rows.Scan(&id, &sel); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id.String())
		if sel {
			selected = id.String()
		}
	}
	return ids, selected
}

// ageTake moves a take's creation back past the take TTL.
func ageTake(t *testing.T, takeID string, days int) {
	t.Helper()
	if _, err := ownerPool(t).Exec(context.Background(), `UPDATE scene_takes SET created_at = now() - make_interval(days => $2) WHERE id = $1`, takeID, days); err != nil {
		t.Fatal(err)
	}
}

// takeAssetKey returns the storage key of a take's asset.
func takeAssetKey(t *testing.T, takeID string) (assetID uuid.UUID, key string) {
	t.Helper()
	if err := ownerPool(t).QueryRow(context.Background(), `SELECT a.id, a.storage_key FROM scene_takes t JOIN assets a ON a.id = t.asset_id WHERE t.id = $1`, takeID).Scan(&assetID, &key); err != nil {
		t.Fatal(err)
	}
	return assetID, key
}

// requireTakeGone checks that a cleaned take lost its row, its asset row
// and its object.
func requireTakeGone(t *testing.T, takeID string, assetID uuid.UUID, key string) {
	t.Helper()
	ctx := context.Background()
	var rows int
	if err := ownerPool(t).QueryRow(ctx, `SELECT (SELECT count(*) FROM scene_takes WHERE id = $1) + (SELECT count(*) FROM assets WHERE id = $2)`, takeID, assetID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("take %s or its asset is still in the database", takeID)
	}
	if _, err := testStorage(t).Stat(ctx, key); err == nil {
		t.Fatalf("object %s is still stored", key)
	}
}

func previewTakeIDs(p cleanupPreviewDTO) []string {
	var ids []string
	for _, tk := range p.Takes {
		ids = append(ids, tk.ID)
	}
	slices.Sort(ids)
	return ids
}

func TestLibraryCleanupPreviewConfirmAndDailyTTL(t *testing.T) {
	skipIfAPIUnreachable(t)
	f := newRenderFixture(t)
	ctx := context.Background()
	sess := f.sess
	scene := f.scenes[0].ID

	// Two more image takes: the newest is selected, the older two are
	// unselected and in no manifest.
	f.regen(t, scene, "image")
	f.regen(t, scene, "image")
	takes, selected := imageTakes(t, f, scene)
	if len(takes) != 3 || selected != takes[2] {
		t.Fatalf("expected three image takes with the newest selected, got %v (selected %s)", takes, selected)
	}

	var settings struct {
		SegmentTTLDays int `json:"segmentTtlDays"`
		TakeTTLDays    int `json:"takeTtlDays"`
	}
	sessionJSON(t, sess.do(http.MethodPut, "/library/settings", map[string]any{"segmentTtlDays": 30, "takeTtlDays": 7}), http.StatusOK, &settings)
	if settings.TakeTTLDays != 7 {
		t.Fatalf("settings = %+v", settings)
	}
	requireStatus(t, sess.do(http.MethodPut, "/library/settings", map[string]any{"segmentTtlDays": 0, "takeTtlDays": 7}), http.StatusBadRequest)

	var page struct {
		Items []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"items"`
	}
	sessionJSON(t, sess.do(http.MethodGet, "/library/assets?limit=100", nil), http.StatusOK, &page)
	if len(page.Items) == 0 {
		t.Fatal("the library lists no assets for a generated episode")
	}
	var usage struct {
		TotalBytes int64 `json:"totalBytes"`
	}
	sessionJSON(t, sess.do(http.MethodGet, "/library/usage", nil), http.StatusOK, &usage)
	if usage.TotalBytes <= 0 {
		t.Fatalf("usage = %+v", usage)
	}

	// Nothing has expired yet: a confirm has nothing to delete.
	var p cleanupPreviewDTO
	sessionJSON(t, sess.do(http.MethodPost, "/library/cleanup/preview", nil), http.StatusOK, &p)
	if len(p.Takes) != 0 || len(p.Segments) != 0 {
		t.Fatalf("fresh takes must not be candidates: %+v", p)
	}
	requireStatus(t, sess.do(http.MethodPost, "/library/cleanup", map[string]any{"token": p.Token}), http.StatusUnprocessableEntity)

	// The dry run lists exactly the expired unselected take.
	ageTake(t, takes[0], 10)
	sessionJSON(t, sess.do(http.MethodPost, "/library/cleanup/preview", nil), http.StatusOK, &p)
	if got := previewTakeIDs(p); !slices.Equal(got, []string{takes[0]}) || p.Bytes <= 0 {
		t.Fatalf("preview = %v (%d bytes), want only %s", got, p.Bytes, takes[0])
	}
	stale := p.Token

	// Another take expires before the confirm: the old token is refused.
	ageTake(t, takes[1], 10)
	requireStatus(t, sess.do(http.MethodPost, "/library/cleanup", map[string]any{"token": stale}), http.StatusConflict)

	sessionJSON(t, sess.do(http.MethodPost, "/library/cleanup/preview", nil), http.StatusOK, &p)
	want := []string{takes[0], takes[1]}
	slices.Sort(want)
	if got := previewTakeIDs(p); !slices.Equal(got, want) {
		t.Fatalf("preview after the change = %v, want %v", got, want)
	}
	asset0, key0 := takeAssetKey(t, takes[0])
	asset1, key1 := takeAssetKey(t, takes[1])
	var started struct {
		RunID uuid.UUID `json:"runId"`
		Takes int       `json:"takes"`
	}
	sessionJSON(t, sess.do(http.MethodPost, "/library/cleanup", map[string]any{"token": p.Token}), http.StatusAccepted, &started)
	if started.Takes != 2 {
		t.Fatalf("cleanup started = %+v", started)
	}
	waitRunDone(t, f.pool, started.RunID, 2*time.Minute)
	requireTakeGone(t, takes[0], asset0, key0)
	requireTakeGone(t, takes[1], asset1, key1)
	if takes, selected = imageTakes(t, f, scene); len(takes) != 1 || selected != takes[0] {
		t.Fatalf("the selected take must survive the cleanup, got %v (selected %s)", takes, selected)
	}
	var audits int
	if err := ownerPool(t).QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'library.cleanup'`, f.fx.TenantID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("expected one library.cleanup audit entry, got %d", audits)
	}

	// Daily TTL: the scheduler enqueues a cleanup for the due tenant and
	// the worker deletes the take that expired since.
	f.regen(t, scene, "image")
	takes, _ = imageTakes(t, f, scene)
	expired := takes[0]
	ageTake(t, expired, 10)
	assetE, keyE := takeAssetKey(t, expired)
	if _, err := ownerPool(t).Exec(ctx, `UPDATE library_settings SET last_cleanup_at = NULL WHERE tenant_id = $1`, f.fx.TenantID); err != nil {
		t.Fatal(err)
	}
	svc := &library.Service{Queries: dbgen.New(f.pool), Engine: f.engine}
	schedCtx, stop := context.WithCancel(ctx)
	defer stop()
	go svc.RunScheduler(schedCtx)
	var ttlRun uuid.UUID
	waitFor(t, time.Minute, "the daily cleanup run for the tenant", func() bool {
		err := f.pool.QueryRow(ctx, `SELECT id FROM pipeline_runs WHERE tenant_id = $1 AND kind = $2 ORDER BY id DESC LIMIT 1`, f.fx.TenantID, library.KindTTLCleanup).Scan(&ttlRun)
		return err == nil
	})
	stop()
	waitRunDone(t, f.pool, ttlRun, 2*time.Minute)
	requireTakeGone(t, expired, assetE, keyE)
	if err := ownerPool(t).QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'library.ttl_cleanup'`, f.fx.TenantID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("expected one library.ttl_cleanup audit entry, got %d", audits)
	}
}
