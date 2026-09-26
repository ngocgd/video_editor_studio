package render

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loomtale/api/internal/scenes"
)

type restartLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *restartLog) record(_ context.Context, _, episodeID uuid.UUID, lang string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, episodeID.String()+"/"+lang)
	return nil
}

func (l *restartLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

func TestSupersederDebouncesPerEpisodeLanguage(t *testing.T) {
	var log restartLog
	s := &Superseder{Delay: 40 * time.Millisecond, restart: log.record}
	tenant, epA, epB := uuid.New(), uuid.New(), uuid.New()
	scene := []uuid.UUID{uuid.New()}
	for range 5 {
		_ = s.Handle(context.Background(), scenes.Changed{TenantID: tenant, EpisodeID: epA, Lang: "en", SceneIDs: scene})
		time.Sleep(5 * time.Millisecond)
	}
	_ = s.Handle(context.Background(), scenes.Changed{TenantID: tenant, EpisodeID: epA, Lang: "vi", SceneIDs: scene})
	_ = s.Handle(context.Background(), scenes.Changed{TenantID: tenant, EpisodeID: epB, Lang: "en", SceneIDs: scene})

	deadline := time.Now().Add(2 * time.Second)
	for log.count() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond) // no late duplicate
	s.Stop()
	if got := log.count(); got != 3 {
		t.Fatalf("restarts = %d (%v), want one per episode language", got, log.calls)
	}
}

func TestSupersederStopDropsPendingRestarts(t *testing.T) {
	var log restartLog
	s := &Superseder{Delay: time.Hour, restart: log.record}
	_ = s.Handle(context.Background(), scenes.Changed{TenantID: uuid.New(), EpisodeID: uuid.New(), Lang: "en", SceneIDs: []uuid.UUID{uuid.New()}})
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop waited for a pending restart")
	}
	_ = s.Handle(context.Background(), scenes.Changed{TenantID: uuid.New(), EpisodeID: uuid.New(), Lang: "en", SceneIDs: []uuid.UUID{uuid.New()}})
	if log.count() != 0 {
		t.Fatalf("restarts after Stop: %v", log.calls)
	}
}
