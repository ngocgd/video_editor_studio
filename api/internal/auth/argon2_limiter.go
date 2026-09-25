package auth

// HashLimiter bounds concurrent argon2id work. Each hash allocates up to
// passwordParams.memoryKiB (64MiB) and holds it for the duration of the
// call; unbounded concurrency from pre-auth /auth/login requests (each
// one runs a hash, including the constant-time decoy for an unknown
// email) is a memory-exhaustion DoS against the whole process.
type HashLimiter struct {
	sem chan struct{}
}

// NewHashLimiter builds a limiter allowing at most maxConcurrent
// in-flight argon2id calls; maxConcurrent < 1 is treated as 1.
func NewHashLimiter(maxConcurrent int) *HashLimiter {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &HashLimiter{sem: make(chan struct{}, maxConcurrent)}
}

// TryAcquire reports whether a slot was obtained without blocking. The
// caller must call Release exactly once for every successful TryAcquire.
func (l *HashLimiter) TryAcquire() bool {
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release returns a slot acquired by TryAcquire.
func (l *HashLimiter) Release() { <-l.sem }
