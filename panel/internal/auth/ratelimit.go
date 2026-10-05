package auth

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// LoginLimiter throttles login attempts per key (client IP + username).
// Entries idle for an hour are evicted lazily to bound memory.
type LoginLimiter struct {
	mu       sync.Mutex
	entries  map[string]*loginAttempt
	per      time.Duration // interval between allowed attempts
	burst    int
	evictFor time.Duration
}

type loginAttempt struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func NewLoginLimiter(per time.Duration, burst int) *LoginLimiter {
	return &LoginLimiter{
		entries:  map[string]*loginAttempt{},
		per:      per,
		burst:    burst,
		evictFor: time.Hour,
	}
}

// Allow reports whether an attempt under key may proceed.
func (l *LoginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	for k, e := range l.entries {
		if now.Sub(e.lastSeen) > l.evictFor {
			delete(l.entries, k)
		}
	}

	e, ok := l.entries[key]
	if !ok {
		e = &loginAttempt{limiter: rate.NewLimiter(rate.Every(l.per), l.burst)}
		l.entries[key] = e
	}
	e.lastSeen = now
	return e.limiter.Allow()
}

// Reset clears the counter after a successful login so a legit user who
// misremembered a few times is not punished further.
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}
