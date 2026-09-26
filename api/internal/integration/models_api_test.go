//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"loomtale/api/internal/db/gen"
)

type modelInfo struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	BytesTotal int64  `json:"bytesTotal"`
	Licence    struct {
		Spdx    string `json:"spdx"`
		Allowed bool   `json:"allowed"`
	} `json:"licence"`
}

func findModel(t *testing.T, items []modelInfo, name string) modelInfo {
	t.Helper()
	for _, m := range items {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("model %s missing from /models", name)
	return modelInfo{}
}

// resetModelInstall removes any install row a previous run left, since
// model installs are deployment-wide rather than per tenant.
func resetModelInstall(t *testing.T, name string) {
	t.Helper()
	pool := ownerPool(t)
	if _, err := pool.Exec(context.Background(), "DELETE FROM model_installs WHERE name = $1", name); err != nil {
		t.Fatalf("reset model_installs: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM model_installs WHERE name = $1", name)
	})
}

// refundLoginIPBudget runs after each test here and resets the per-IP
// login bucket: every test in this package logs in from the same client
// IP, whose bucket holds only 20 logins an hour, so the logins these
// tests make would otherwise starve the tests that run after them.
func refundLoginIPBudget(t *testing.T) {
	t.Helper()
	pool := ownerPool(t)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DELETE FROM rate_limit_buckets WHERE bucket_key LIKE 'login:ip:%'"); err != nil {
			t.Errorf("refund login budget: %v", err)
		}
	})
}

func TestModelsListShowsManifestAndBlockedLicences(t *testing.T) {
	skipIfAPIUnreachable(t)
	refundLoginIPBudget(t)
	q := gen.New(ownerPool(t))
	viewer := createFixtureUser(t, q, "models-viewer", uniqueEmail("models-viewer"), "viewer")
	sess := login(t, viewer.Email, viewer.Password)

	resp := sess.do(http.MethodGet, "/models", nil)
	requireStatus(t, resp, http.StatusOK)
	var list struct {
		Items []modelInfo `json:"items"`
	}
	decodeJSON(t, resp, &list)

	blocked := findModel(t, list.Items, "illustrious-xl-v1.1")
	if blocked.Status != "blocked" || blocked.Licence.Allowed {
		t.Fatalf("a non-allowlisted licence must be listed as blocked, got %+v", blocked)
	}
	zimage := findModel(t, list.Items, "z-image-turbo")
	if !zimage.Licence.Allowed || zimage.Licence.Spdx != "Apache-2.0" || zimage.BytesTotal <= 0 {
		t.Fatalf("unexpected z-image-turbo entry %+v", zimage)
	}
}

func TestModelInstallIsOwnerOnlyAndRefusesBlockedLicences(t *testing.T) {
	skipIfAPIUnreachable(t)
	refundLoginIPBudget(t)
	q := gen.New(ownerPool(t))
	owner := createFixtureUser(t, q, "models-owner-gate", uniqueEmail("models-owner"), "owner")
	editor := createFixtureUser(t, q, "models-editor-gate", uniqueEmail("models-editor"), "editor")

	requireStatus(t, login(t, editor.Email, editor.Password).do(http.MethodPost, "/models/z-image-turbo/install", nil), http.StatusForbidden)

	sess := login(t, owner.Email, owner.Password)
	requireProblem(t, sess.do(http.MethodPost, "/models/illustrious-xl-v1.1/install", nil), http.StatusUnprocessableEntity, "licence not allowed")
	requireProblem(t, sess.do(http.MethodPost, "/models/illustrious-xl-v1.1/load", nil), http.StatusUnprocessableEntity, "licence not allowed")
	requireProblem(t, sess.do(http.MethodPost, "/models/no-such-model/install", nil), http.StatusNotFound, "unknown model")
}

func TestModelInstallQueuesPullStepAndPauseCancelsIt(t *testing.T) {
	skipIfAPIUnreachable(t)
	refundLoginIPBudget(t)
	resetModelInstall(t, "qwen-image")
	pool := ownerPool(t)
	q := gen.New(pool)
	owner := createFixtureUser(t, q, "models-owner-pull", uniqueEmail("models-owner"), "owner")
	sess := login(t, owner.Email, owner.Password)

	resp := sess.do(http.MethodPost, "/models/qwen-image/install", nil)
	requireStatus(t, resp, http.StatusAccepted)
	var installed modelInfo
	decodeJSON(t, resp, &installed)
	if installed.Status != "downloading" {
		t.Fatalf("install must report downloading, got %s", installed.Status)
	}
	requireProblem(t, sess.do(http.MethodPost, "/models/qwen-image/install", nil), http.StatusConflict, "download already running")

	var stepID uuid.UUID
	var kind, queue, status string
	var priority int
	err := pool.QueryRow(context.Background(), `
		SELECT s.id, s.kind, s.queue, s.priority, s.status
		FROM model_installs m JOIN pipeline_steps s ON s.id = m.step_id
		WHERE m.name = 'qwen-image'`).Scan(&stepID, &kind, &queue, &priority, &status)
	if err != nil {
		t.Fatalf("pull step not recorded on the install row: %v", err)
	}
	if kind != "models.pull" || queue != "io" || priority != 4 {
		t.Fatalf("pull step is %s on %s at priority %d, want models.pull on io at 4", kind, queue, priority)
	}

	resp = sess.do(http.MethodPost, "/models/qwen-image/pause", nil)
	requireStatus(t, resp, http.StatusOK)
	var paused modelInfo
	decodeJSON(t, resp, &paused)
	if paused.Status != "paused" {
		t.Fatalf("pause must report paused, got %s", paused.Status)
	}
	if err := pool.QueryRow(context.Background(), "SELECT status FROM pipeline_steps WHERE id = $1", stepID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "canceled" {
		t.Fatalf("pausing must cancel the pull step, got %s", status)
	}
	requireProblem(t, sess.do(http.MethodPost, "/models/qwen-image/pause", nil), http.StatusConflict, "no download is running")

	// Resuming is a new install run.
	requireStatus(t, sess.do(http.MethodPost, "/models/qwen-image/install", nil), http.StatusAccepted)
}

func TestModelLoadNeedsInstallAndUnloadQueuesGPUStep(t *testing.T) {
	skipIfAPIUnreachable(t)
	refundLoginIPBudget(t)
	resetModelInstall(t, "z-image-turbo")
	pool := ownerPool(t)
	q := gen.New(pool)
	editor := createFixtureUser(t, q, "models-editor-load", uniqueEmail("models-editor"), "editor")
	sess := login(t, editor.Email, editor.Password)

	requireProblem(t, sess.do(http.MethodPost, "/models/z-image-turbo/load", nil), http.StatusConflict, "model is not installed")

	resp := sess.do(http.MethodPost, "/models/unload", nil)
	requireStatus(t, resp, http.StatusAccepted)
	var result struct {
		StepID uuid.UUID `json:"stepId"`
	}
	decodeJSON(t, resp, &result)
	var kind, queue string
	var priority int
	if err := pool.QueryRow(context.Background(), "SELECT kind, queue, priority FROM pipeline_steps WHERE id = $1", result.StepID).Scan(&kind, &queue, &priority); err != nil {
		t.Fatal(err)
	}
	if kind != "models.unload" || queue != "gpu" || priority != 1 {
		t.Fatalf("unload step is %s on %s at %d, want models.unload on gpu at 1", kind, queue, priority)
	}
	// Leave no queued gpu step behind for other tests' GPU queue views.
	if _, err := pool.Exec(context.Background(), "UPDATE pipeline_steps SET status = 'canceled' WHERE id = $1", result.StepID); err != nil {
		t.Fatal(err)
	}
}
