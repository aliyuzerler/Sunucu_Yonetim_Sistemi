package identity

import (
	"sync"
	"time"
)

// RateLimiter is a fixed-window counter used for login/enrollment throttling.
// In-memory by design: the control plane is initially a single instance and
// the limiter is a brute-force brake, not a distributed quota. If HA arrives,
// this moves to a shared store (tracked in project-status.md).
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
	window  time.Duration
	max     int
	now     func() time.Time
}

type rateBucket struct {
	startedAt time.Time
	count     int
}

// NewRateLimiter allows at most max events per sliding window per key.
func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		buckets: map[string]*rateBucket{},
		window:  window,
		max:     max,
		now:     time.Now,
	}
}

// Allow reports whether an event for key is permitted and records it when so.
func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	b, ok := l.buckets[key]
	if !ok || now.Sub(b.startedAt) >= l.window {
		// Opportunistic cleanup keeps the map bounded under adversarial keys.
		if len(l.buckets) > 10_000 {
			l.cleanupLocked(now)
		}
		l.buckets[key] = &rateBucket{startedAt: now, count: 1}
		return true
	}
	if b.count >= l.max {
		return false
	}
	b.count++
	return true
}

// Reset clears the counter for a key (e.g. after a successful login).
func (l *RateLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

func (l *RateLimiter) cleanupLocked(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.startedAt) >= l.window {
			delete(l.buckets, k)
		}
	}
}
