package story

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// maxInteractiveOutlines is how many outline steps of one series-generate
// run keep interactive priority. The rest run at batch priority, so a
// 500-episode request cannot crowd the LLM queue ahead of writer actions.
const maxInteractiveOutlines = 20

// maxOutlineInsertAttempts bounds how often an outline retries its insert
// with a fresh episode number after another writer took the planned one.
// Each retry is a cheap insert: the LLM result is reused, never regenerated.
const maxOutlineInsertAttempts = 5

// episodesUniqueIdx is the unique index on (series_id, idx).
const episodesUniqueIdx = "episodes_series_id_idx_key"

// OutlineInput is an outline step's Input: the episode number planned for
// it at enqueue time, so sibling steps of one run prompt for different
// episodes instead of all asking for the same next number.
type OutlineInput struct {
	EpisodeIdx int32 `json:"episodeIdx,omitempty"`
}

// outlineStepSpecs builds the outline steps of a series-generate run. Each
// step gets its own episode number, counting up from firstIdx, and only the
// first maxInteractiveOutlines steps run at interactive priority.
func outlineStepSpecs(seriesID, dependsOn uuid.UUID, firstIdx int32, count int) ([]pipeline.StepSpec, error) {
	steps := make([]pipeline.StepSpec, 0, count)
	for i := 0; i < count; i++ {
		input, err := json.Marshal(OutlineInput{EpisodeIdx: firstIdx + int32(i)})
		if err != nil {
			return nil, err
		}
		priority := pipeline.PriorityInteractive
		if i >= maxInteractiveOutlines {
			priority = pipeline.PriorityBatch
		}
		steps = append(steps, pipeline.StepSpec{
			ID:        idconv.NewV7(),
			Kind:      KindOutline,
			ScopeKind: ScopeSeries,
			ScopeID:   seriesID,
			Priority:  priority,
			DependsOn: []uuid.UUID{dependsOn},
			Input:     input,
		})
	}
	return steps, nil
}

// episodeInserter is the slice of dbgen.Queries the outline insert needs.
type episodeInserter interface {
	CreateEpisode(ctx context.Context, arg dbgen.CreateEpisodeParams) (dbgen.Episode, error)
	NextEpisodeIdx(ctx context.Context, arg dbgen.NextEpisodeIdxParams) (int32, error)
}

// insertOutlinedEpisode inserts the outlined episode at plannedIdx. When a
// concurrent import, manual create or sibling outline already took that
// number, it moves to the series' next free number and tries again, so a
// collision never throws away (and re-bills) the generated outline.
func insertOutlinedEpisode(ctx context.Context, q episodeInserter, tenantID, seriesID uuid.UUID, plannedIdx int32, outline []byte) (dbgen.Episode, error) {
	idx := plannedIdx
	for attempt := 1; ; attempt++ {
		episode, err := q.CreateEpisode(ctx, dbgen.CreateEpisodeParams{
			ID:       idconv.ToPg(idconv.NewV7()),
			TenantID: idconv.ToPg(tenantID),
			SeriesID: idconv.ToPg(seriesID),
			Idx:      idx,
			Title:    fmt.Sprintf("Episode %d", idx),
			Outline:  outline,
			Status:   "outlined",
		})
		if err == nil || !isEpisodeIdxTaken(err) || attempt >= maxOutlineInsertAttempts {
			return episode, err
		}
		next, err := q.NextEpisodeIdx(ctx, dbgen.NextEpisodeIdxParams{TenantID: idconv.ToPg(tenantID), SeriesID: idconv.ToPg(seriesID)})
		if err != nil {
			return dbgen.Episode{}, err
		}
		if next <= idx {
			next = idx + 1
		}
		idx = next
	}
}

// isEpisodeIdxTaken reports a unique violation on (series_id, idx).
func isEpisodeIdxTaken(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == episodesUniqueIdx
}
