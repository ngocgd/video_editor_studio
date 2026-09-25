package sse

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"
)

// BenchmarkHubBroadcast exercises the fan-out path (Hub.broadcast ->
// Subscriber.deliver) at the scale the performance budget names: 200
// subscribers, each watching one shared run. It does not open a real
// Postgres LISTEN connection (Hub.Run is not started); it drives
// broadcast directly, which is the part any throughput ceiling would
// actually be in.
func BenchmarkHubBroadcast(b *testing.B) {
	// Hub.Run/listenOnce (the real Postgres LISTEN connection) is not
	// exercised by this benchmark, so a nil pool is safe.
	h := NewHub(nil)
	tenantID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const subscribers = 200
	drains := make([]chan struct{}, subscribers)
	for i := 0; i < subscribers; i++ {
		sub, err := h.Subscribe(ctx, uuid.New(), tenantID, map[string]bool{"run-1": true})
		if err != nil {
			b.Fatal(err)
		}
		done := make(chan struct{})
		drains[i] = done
		go func(s *Subscriber, done chan struct{}) {
			for range s.out {
				// Drain as fast as possible; this benchmark measures the
				// hub's fan-out cost, not a slow-consumer scenario.
			}
			close(done)
		}(sub, done)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.broadcast(Event{
			Type: "step", StepID: strconv.Itoa(i), RunID: "run-1", TenantID: tenantID.String(),
			Status: "running", Progress: int16(i % 100),
		})
	}
	b.StopTimer()
	cancel()
	for _, d := range drains {
		<-d
	}
}
