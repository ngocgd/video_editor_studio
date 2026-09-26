package story

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/pipeline"
)

// fakeEpisodes rejects inserts at taken numbers the way the unique index does.
type fakeEpisodes struct {
	taken   map[int32]bool
	inserts []int32
}

func (f *fakeEpisodes) CreateEpisode(_ context.Context, arg dbgen.CreateEpisodeParams) (dbgen.Episode, error) {
	f.inserts = append(f.inserts, arg.Idx)
	if f.taken[arg.Idx] {
		return dbgen.Episode{}, &pgconn.PgError{Code: "23505", ConstraintName: episodesUniqueIdx}
	}
	f.taken[arg.Idx] = true
	return dbgen.Episode{Idx: arg.Idx, Title: arg.Title}, nil
}

func (f *fakeEpisodes) NextEpisodeIdx(context.Context, dbgen.NextEpisodeIdxParams) (int32, error) {
	var highest int32
	for idx := range f.taken {
		if idx > highest {
			highest = idx
		}
	}
	return highest + 1, nil
}

func TestOutlinedEpisodeMovesToTheNextFreeNumberWhenItsNumberIsTaken(t *testing.T) {
	q := &fakeEpisodes{taken: map[int32]bool{3: true, 4: true}}
	episode, err := insertOutlinedEpisode(context.Background(), q, uuid.New(), uuid.New(), 3, []byte("[]"))
	if err != nil {
		t.Fatal(err)
	}
	if episode.Idx != 5 || episode.Title != "Episode 5" {
		t.Fatalf("episode = %d %q, want 5 \"Episode 5\"", episode.Idx, episode.Title)
	}
	if len(q.inserts) != 2 {
		t.Fatalf("inserts = %v, want the planned number then the next free one", q.inserts)
	}
}

func TestOutlinedEpisodeGivesUpAfterBoundedCollisions(t *testing.T) {
	q := &alwaysTaken{}
	_, err := insertOutlinedEpisode(context.Background(), q, uuid.New(), uuid.New(), 1, []byte("[]"))
	if !isEpisodeIdxTaken(err) {
		t.Fatalf("err = %v, want the unique violation", err)
	}
	if q.inserts != maxOutlineInsertAttempts {
		t.Fatalf("inserts = %d, want %d", q.inserts, maxOutlineInsertAttempts)
	}
}

func TestOutlinedEpisodeDoesNotRetryOtherErrors(t *testing.T) {
	q := &alwaysTaken{err: errors.New("connection reset")}
	if _, err := insertOutlinedEpisode(context.Background(), q, uuid.New(), uuid.New(), 1, nil); err == nil {
		t.Fatal("expected the insert error")
	}
	if q.inserts != 1 {
		t.Fatalf("inserts = %d, want 1", q.inserts)
	}
}

type alwaysTaken struct {
	inserts int
	err     error
}

func (a *alwaysTaken) CreateEpisode(context.Context, dbgen.CreateEpisodeParams) (dbgen.Episode, error) {
	a.inserts++
	if a.err != nil {
		return dbgen.Episode{}, a.err
	}
	return dbgen.Episode{}, &pgconn.PgError{Code: "23505", ConstraintName: episodesUniqueIdx}
}

func (a *alwaysTaken) NextEpisodeIdx(context.Context, dbgen.NextEpisodeIdxParams) (int32, error) {
	return 1, nil
}

func TestSeriesOutlineStepsPlanDistinctEpisodesAndCapInteractivePriority(t *testing.T) {
	steps, err := outlineStepSpecs(uuid.New(), uuid.New(), 4, maxInteractiveOutlines+5)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range steps {
		var in OutlineInput
		if err := json.Unmarshal(s.Input, &in); err != nil {
			t.Fatal(err)
		}
		if in.EpisodeIdx != int32(4+i) {
			t.Fatalf("step %d plans episode %d, want %d", i, in.EpisodeIdx, 4+i)
		}
		want := pipeline.PriorityInteractive
		if i >= maxInteractiveOutlines {
			want = pipeline.PriorityBatch
		}
		if s.Priority != want {
			t.Fatalf("step %d priority = %d, want %d", i, s.Priority, want)
		}
	}
}
