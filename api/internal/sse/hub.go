package sse

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxStreamsPerUser bounds how many concurrent /events connections one
// user may hold; the web client shares a single stream across browser
// tabs (phase 5), so 6 comfortably covers a user with several tabs open
// without letting a runaway client exhaust LISTEN fan-out capacity.
const MaxStreamsPerUser = 6

// ErrTooManyStreams is returned by Subscribe when userID already holds
// MaxStreamsPerUser open connections.
var ErrTooManyStreams = errors.New("sse: too many open streams for this user")

// listenReconnectBackoff is how long Hub.Run waits before retrying LISTEN
// after the connection drops.
const listenReconnectBackoff = 2 * time.Second

// Hub owns one Postgres LISTEN connection per process and fans decoded
// events out to every matching Subscriber. Run must be started once (by
// cmd/api) before any Subscribe call can receive live events.
type Hub struct {
	pool *pgxpool.Pool

	mu          sync.Mutex
	subscribers map[string]*Subscriber
	byUser      map[uuid.UUID]int
}

// NewHub builds a Hub bound to pool. pool is used both for the dedicated
// LISTEN connection (acquired and held for the process lifetime, with
// reconnect on failure) and is otherwise untouched by this package.
func NewHub(pool *pgxpool.Pool) *Hub {
	return &Hub{
		pool:        pool,
		subscribers: make(map[string]*Subscriber),
		byUser:      make(map[uuid.UUID]int),
	}
}

// Subscribe registers a new subscriber and starts its delivery loop,
// bound to ctx (typically the HTTP request's context: it ends when the
// client disconnects). topics is the set of run ids (as strings) this
// connection is allowed to receive events for; the caller is responsible
// for having already authorised each one against the caller's tenant.
func (h *Hub) Subscribe(ctx context.Context, userID, tenantID uuid.UUID, topics map[string]bool) (*Subscriber, error) {
	h.mu.Lock()
	if h.byUser[userID] >= MaxStreamsPerUser {
		h.mu.Unlock()
		return nil, ErrTooManyStreams
	}
	sub := newSubscriber(userID, tenantID, topics)
	h.subscribers[sub.ID] = sub
	h.byUser[userID]++
	h.mu.Unlock()

	go sub.run(ctx)
	go func() {
		<-ctx.Done()
		h.unsubscribe(sub)
	}()
	return sub, nil
}

func (h *Hub) unsubscribe(sub *Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subscribers[sub.ID]; !ok {
		return
	}
	delete(h.subscribers, sub.ID)
	h.byUser[sub.UserID]--
	if h.byUser[sub.UserID] <= 0 {
		delete(h.byUser, sub.UserID)
	}
}

// broadcast fans evt out to every subscriber it is in scope for.
func (h *Hub) broadcast(evt Event) {
	h.mu.Lock()
	targets := make([]*Subscriber, 0, len(h.subscribers))
	for _, sub := range h.subscribers {
		if sub.allows(evt) {
			targets = append(targets, sub)
		}
	}
	h.mu.Unlock()
	for _, sub := range targets {
		sub.deliver(evt)
	}
}

// resyncAll asks every currently connected subscriber to resync: used
// when the LISTEN connection itself was lost and reconnected, since any
// NOTIFY that fired during the gap is otherwise silently missed.
func (h *Hub) resyncAll() {
	h.mu.Lock()
	targets := make([]*Subscriber, 0, len(h.subscribers))
	for _, sub := range h.subscribers {
		targets = append(targets, sub)
	}
	h.mu.Unlock()
	for _, sub := range targets {
		sub.forceResync()
	}
}

// Run holds the LISTEN connection and reconnects with resyncAll on any
// failure. It blocks until ctx is cancelled and must be started exactly
// once per process, typically in a background goroutine from cmd/api.
func (h *Hub) Run(ctx context.Context) {
	first := true
	for ctx.Err() == nil {
		if !first {
			h.resyncAll()
		}
		first = false
		if err := h.listenOnce(ctx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "sse: LISTEN connection lost, reconnecting", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(listenReconnectBackoff):
			}
		}
	}
}

func (h *Hub) listenOnce(ctx context.Context) error {
	conn, err := h.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN "+NotifyChannel); err != nil {
		return err
	}
	slog.InfoContext(ctx, "sse: listening for pipeline events")

	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var evt Event
		if err := json.Unmarshal([]byte(notification.Payload), &evt); err != nil {
			slog.ErrorContext(ctx, "sse: failed to decode notify payload", "error", err)
			continue
		}
		h.broadcast(evt)
	}
}
