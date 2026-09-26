package render

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/scenes"
)

// SupersedeDebounce is how long an episode language must stay unedited
// before its running render is restarted, so a burst of edits costs one
// restart.
const SupersedeDebounce = 5 * time.Second

// supersedeTimeout bounds one restart (freeze plus enqueue).
const supersedeTimeout = 2 * time.Minute

type supersedeKey struct {
	Tenant  uuid.UUID
	Episode uuid.UUID
	Lang    string
}

type pendingRestart struct{ timer *time.Timer }

// Superseder restarts an episode language's active render when one of
// its scenes changes: after the debounce it freezes a fresh manifest
// (unchanged segments are cache hits, so only the edited scene and its
// transitions are encoded again) and supersedes the old run with the new
// one. Register Handle on scenes.Hooks in every process that mutates
// scenes (the api and the workers that record takes).
type Superseder struct {
	Freezer *Freezer
	// Delay overrides SupersedeDebounce (tests).
	Delay time.Duration
	// restart replaces Restart (tests).
	restart func(ctx context.Context, tenantID, episodeID uuid.UUID, lang string) error

	mu      sync.Mutex
	pending map[supersedeKey]*pendingRestart
	wg      sync.WaitGroup
	stopped bool
}

// Handle is the scenes.ChangedHandler: it only (re)arms the episode
// language's debounce timer and never blocks the mutation.
func (s *Superseder) Handle(_ context.Context, evt scenes.Changed) error {
	key := supersedeKey{Tenant: evt.TenantID, Episode: evt.EpisodeID, Lang: evt.Lang}
	delay := s.Delay
	if delay <= 0 {
		delay = SupersedeDebounce
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil
	}
	if s.pending == nil {
		s.pending = map[supersedeKey]*pendingRestart{}
	}
	if old := s.pending[key]; old != nil && old.timer.Stop() {
		s.wg.Done() // its callback will never run
	}
	p := &pendingRestart{}
	s.pending[key] = p
	s.wg.Add(1)
	p.timer = time.AfterFunc(delay, func() {
		defer s.wg.Done()
		s.mu.Lock()
		current := s.pending[key] == p
		if current {
			delete(s.pending, key)
		}
		s.mu.Unlock()
		if !current {
			return // a later edit re-armed the timer
		}
		ctx, cancel := context.WithTimeout(context.Background(), supersedeTimeout)
		defer cancel()
		restart := s.Restart
		if s.restart != nil {
			restart = s.restart
		}
		if err := restart(ctx, key.Tenant, key.Episode, key.Lang); err != nil {
			slog.WarnContext(ctx, "render: restart after edit failed", "error", err, "episode_id", key.Episode, "lang", key.Lang)
		}
	})
	return nil
}

// Stop cancels every pending restart and waits for running ones.
func (s *Superseder) Stop() {
	s.mu.Lock()
	s.stopped = true
	for key, p := range s.pending {
		if p.timer.Stop() {
			s.wg.Done() // the callback will never run
		}
		delete(s.pending, key)
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// Restart supersedes the episode language's active render run, if any,
// with a run from a freshly frozen manifest. When the edit left the
// episode unrenderable (a scene lost its image, say) the old run is
// canceled instead: it would only finish a render of content the user
// has already changed.
func (s *Superseder) Restart(ctx context.Context, tenantID, episodeID uuid.UUID, lang string) error {
	f := s.Freezer
	active, err := f.Queries.GetActiveRenderRun(ctx, dbgen.GetActiveRenderRunParams{
		TenantID: idconv.ToPg(tenantID), EpisodeID: idconv.ToPg(episodeID), Lang: lang,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	oldRun := idconv.FromPg(active.RunID)

	frozen, err := f.Freeze(ctx, FreezeRequest{TenantID: tenantID, EpisodeID: episodeID, Lang: lang, AfterEdit: true})
	if err != nil {
		var notReady *NotReadyError
		if errors.As(err, &notReady) {
			if cerr := f.Engine.CancelRun(ctx, tenantID, oldRun); cerr != nil && !errors.Is(cerr, pipeline.ErrNotFound) {
				return cerr
			}
			slog.InfoContext(ctx, "render: canceled after an edit left the episode unrenderable", "episode_id", episodeID, "lang", lang, "reasons", notReady.Reasons)
			return nil
		}
		return fmt.Errorf("render: freeze after edit: %w", err)
	}

	if err := f.Engine.SupersedeRun(ctx, tenantID, oldRun, frozen.RunID); err != nil {
		if !errors.Is(err, pipeline.ErrNotFound) {
			return err
		}
		// The old run ended while we froze. If another process already
		// superseded it, our run duplicates that restart; otherwise it
		// finished with pre-edit content and our run is the fresh render.
		run, gerr := f.Queries.GetRun(ctx, dbgen.GetRunParams{TenantID: idconv.ToPg(tenantID), ID: active.RunID})
		if gerr == nil && run.SupersededBy.Valid && idconv.FromPg(run.SupersededBy) != frozen.RunID {
			return f.Engine.CancelRun(ctx, tenantID, frozen.RunID)
		}
	}
	slog.InfoContext(ctx, "render: restarted after edit", "episode_id", episodeID, "lang", lang, "run_id", frozen.RunID, "reused", frozen.Reused)
	return nil
}
