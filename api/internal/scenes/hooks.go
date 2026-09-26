package scenes

import (
	"context"
	"log/slog"
	"sync"

	"github.com/google/uuid"
)

// Changed is emitted once per mutation of scene inputs that a render
// depends on: narration, prompt, segments, motion, characters, style, a
// re-split, or a take selection (including a new take becoming selected).
type Changed struct {
	TenantID  uuid.UUID
	EpisodeID uuid.UUID
	Lang      string
	SceneIDs  []uuid.UUID
	// Reason is a short machine-readable cause ("edit", "take", "split").
	Reason string
}

// ChangedHandler reacts to a Changed event. It runs synchronously after
// the mutation committed and must not block for long; an error is logged,
// never propagated to the mutation that already happened.
type ChangedHandler func(ctx context.Context, evt Changed) error

// Hooks is the in-process scenes.Changed registry. The render phase
// registers its supersede handler here; this package never imports it.
type Hooks struct {
	mu       sync.RWMutex
	handlers []ChangedHandler
}

// Register adds a handler.
func (h *Hooks) Register(fn ChangedHandler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handlers = append(h.handlers, fn)
}

// Emit calls every handler with evt. A nil *Hooks is a no-op, so a test
// or tool can use the service without wiring hooks.
func (h *Hooks) Emit(ctx context.Context, evt Changed) {
	if h == nil || len(evt.SceneIDs) == 0 {
		return
	}
	h.mu.RLock()
	handlers := append([]ChangedHandler(nil), h.handlers...)
	h.mu.RUnlock()
	for _, fn := range handlers {
		if err := fn(ctx, evt); err != nil {
			slog.WarnContext(ctx, "scenes: Changed handler failed", "error", err, "episode_id", evt.EpisodeID, "reason", evt.Reason)
		}
	}
}
