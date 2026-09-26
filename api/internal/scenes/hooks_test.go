package scenes

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestHooksCallEveryHandlerAndSurviveErrors(t *testing.T) {
	var h Hooks
	var got []Changed
	h.Register(func(context.Context, Changed) error { return errors.New("boom") })
	h.Register(func(_ context.Context, evt Changed) error { got = append(got, evt); return nil })
	h.Emit(context.Background(), Changed{SceneIDs: []uuid.UUID{uuid.New()}, Reason: "edit"})
	if len(got) != 1 || got[0].Reason != "edit" {
		t.Fatalf("got %+v", got)
	}
	h.Emit(context.Background(), Changed{Reason: "empty"})
	if len(got) != 1 {
		t.Fatal("an event with no scenes must not be delivered")
	}
	var nilHooks *Hooks
	nilHooks.Emit(context.Background(), Changed{SceneIDs: []uuid.UUID{uuid.New()}})
}
