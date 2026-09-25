package ratelimit

import (
	"testing"
	"time"
)

func TestMemoryAllowsUpToCapacityThenBlocks(t *testing.T) {
	m := NewMemory(3, 1)
	fixed := time.Now()
	m.now = func() time.Time { return fixed }

	for i := 0; i < 3; i++ {
		if !m.Allow("k") {
			t.Fatalf("expected request %d to be allowed within capacity", i)
		}
	}
	if m.Allow("k") {
		t.Fatal("expected the 4th immediate request to be blocked")
	}
}

func TestMemoryRefillsOverTime(t *testing.T) {
	m := NewMemory(1, 1) // 1 token/second
	fixed := time.Now()
	m.now = func() time.Time { return fixed }

	if !m.Allow("k") {
		t.Fatal("expected first request to be allowed")
	}
	if m.Allow("k") {
		t.Fatal("expected immediate second request to be blocked")
	}

	fixed = fixed.Add(1100 * time.Millisecond)
	m.now = func() time.Time { return fixed }
	if !m.Allow("k") {
		t.Fatal("expected request to be allowed after refill window")
	}
}

func TestMemoryKeysAreIndependent(t *testing.T) {
	m := NewMemory(1, 0)
	if !m.Allow("a") {
		t.Fatal("expected key a to be allowed")
	}
	if !m.Allow("b") {
		t.Fatal("expected key b (independent bucket) to be allowed")
	}
	if m.Allow("a") {
		t.Fatal("expected key a to be exhausted")
	}
}
