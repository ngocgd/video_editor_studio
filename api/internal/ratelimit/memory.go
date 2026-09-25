package ratelimit

import (
	"net/http"
	"sync"
	"time"
)

// Memory is a per-instance in-memory token bucket limiter, used as general
// API flood defence in front of every route (login/password endpoints
// additionally go through DBBucket, which stays correct across
// instances). Buckets for keys that have not been seen recently are
// garbage-collected lazily on access.
type Memory struct {
	mu              sync.Mutex
	buckets         map[string]*memBucket
	capacity        float64
	refillPerSecond float64
	maxIdle         time.Duration
	now             func() time.Time
}

type memBucket struct {
	tokens     float64
	lastRefill time.Time
}

// NewMemory builds an in-memory limiter with the given capacity and
// refill rate (tokens/second).
func NewMemory(capacity, refillPerSecond float64) *Memory {
	return &Memory{
		buckets:         make(map[string]*memBucket),
		capacity:        capacity,
		refillPerSecond: refillPerSecond,
		maxIdle:         10 * time.Minute,
		now:             time.Now,
	}
}

// Allow refills and consumes one token from the bucket for key.
func (m *Memory) Allow(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	b, ok := m.buckets[key]
	if !ok {
		b = &memBucket{tokens: m.capacity, lastRefill: now}
		m.buckets[key] = b
	}
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		b.tokens = min(m.capacity, b.tokens+elapsed*m.refillPerSecond)
		b.lastRefill = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--

	// Opportunistic cleanup: drop buckets that are back at full capacity
	// and have been idle a while, so a long-running process does not
	// accumulate one entry per distinct client IP forever.
	if len(m.buckets) > 10000 {
		for k, v := range m.buckets {
			if now.Sub(v.lastRefill) > m.maxIdle {
				delete(m.buckets, k)
			}
		}
	}
	return true
}

// Middleware applies Allow keyed by the client IP to every request,
// returning 429 via onReject when the bucket is empty.
func (m *Memory) Middleware(clientIP func(*http.Request) string, onReject http.HandlerFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !m.Allow(clientIP(r)) {
				onReject(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
