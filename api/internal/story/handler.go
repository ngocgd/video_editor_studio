// Package story implements the series/bible/episode/draft/import/AI-action
// slice of the generated strict server interface, plus the pipeline step
// handlers that run the AI writing actions. Everything here is
// tenant-scoped through tenant.MustFromCtx; no query ever accepts a
// client-supplied tenant id.
package story

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/dbpool"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/registry"
	"loomtale/api/internal/storage"
)

// defaultTokenBudget bounds storyctx.Build's truncation when the resolved
// provider has no more specific context-window figure wired in yet; a
// generous, safely-under-every-current-provider's-real-window estimate
// (see storyctx's own charsPerToken doc comment for the heuristic this
// feeds).
const defaultTokenBudget = 100_000

// StoryAPI implements the story-writer slice of gen.StrictServerInterface
// (series, bible, episodes, drafts, ai-actions, imports).
type StoryAPI struct {
	Pool     *dbpool.Pool
	Queries  *dbgen.Queries
	Engine   *pipeline.Engine
	Registry *registry.Registry
	Internal *storage.Internal
}

// isNoRows reports whether err is "no row found", used throughout to map
// a lookup miss to a 404 Problem response.
func isNoRows(err error) bool {
	return err == pgx.ErrNoRows
}

// requireSeriesOwnership loads series by id, scoped to tenantID, and
// returns pgx.ErrNoRows (mapped to 404 by callers) if it does not belong
// to this tenant — the same query shape every other domain package uses,
// so a series in another tenant is indistinguishable from one that does
// not exist at all.
func (h *StoryAPI) requireSeries(ctx context.Context, tenantID, seriesID uuid.UUID) (dbgen.Series, error) {
	return h.Queries.GetSeriesByID(ctx, dbgen.GetSeriesByIDParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(seriesID)})
}

func (h *StoryAPI) requireEpisode(ctx context.Context, tenantID, episodeID uuid.UUID) (dbgen.Episode, error) {
	return h.Queries.GetEpisodeByID(ctx, dbgen.GetEpisodeByIDParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(episodeID)})
}

// userIDPtr returns a pointer to sess's user id for a nullable
// created_by (users(id)) column, or nil when no session user is present
// (should not happen on an authenticated, tenant-scoped route, but
// created_by is ON DELETE SET NULL so a nil is always safe to write).
func userIDPtr(sess authpkg.Session) *uuid.UUID {
	if sess.UserID == uuid.Nil {
		return nil
	}
	id := sess.UserID
	return &id
}
