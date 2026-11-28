package main

import (
	"sync"
	"time"
)

// attemptLimiter counts attempts per key (a client IP) in a sliding window.
// It guards the endpoints that work before sign-in: password-like guesses
// (token, QR key) and pairing requests that put a prompt on the device.
//
// An attempt is reserved before it is checked (reserve) and given back if it
// succeeded (refund), so concurrent requests from one address cannot all
// slip through the last free slot. Memory is bounded: at most maxKeys
// addresses are tracked and expired ones are swept at most once a second;
// while the table is still full, attempts from addresses it does not know
// yet are refused (nothing still inside its window is forgotten).
type attemptLimiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	hits    map[string][]time.Time
	maxKeys int
	swept   time.Time
}

func newAttemptLimiter(max int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{max: max, window: window, hits: map[string][]time.Time{}, maxKeys: 4096}
}

func (l *attemptLimiter) recentLocked(key string, now time.Time) []time.Time {
	h := l.hits[key]
	i := 0
	for i < len(h) && now.Sub(h[i]) >= l.window {
		i++
	}
	h = h[i:]
	if len(h) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = h
	}
	return h
}

// roomLocked reports whether a new key can be added.
func (l *attemptLimiter) roomLocked(now time.Time) bool {
	if len(l.hits) < l.maxKeys {
		return true
	}
	if now.Sub(l.swept) >= time.Second {
		l.swept = now
		for k := range l.hits {
			l.recentLocked(k, now)
		}
	}
	return len(l.hits) < l.maxKeys
}

// blocked reports whether key has used up its attempts, and for how long.
func (l *attemptLimiter) blocked(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h := l.recentLocked(key, now)
	if len(h) < l.max {
		return false, 0
	}
	return true, l.window - now.Sub(h[0])
}

// reserve takes one attempt for key if any is left. The returned time
// identifies the attempt for refund.
func (l *attemptLimiter) reserve(key string, now time.Time) (ok bool, wait time.Duration, slot time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h := l.recentLocked(key, now)
	if len(h) >= l.max {
		return false, l.window - now.Sub(h[0]), time.Time{}
	}
	if len(h) == 0 && !l.roomLocked(now) {
		return false, time.Second, time.Time{}
	}
	l.hits[key] = append(h, now)
	return true, 0, now
}

// refund gives back an attempt that succeeded.
func (l *attemptLimiter) refund(key string, slot time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h := l.hits[key]
	for i := len(h) - 1; i >= 0; i-- {
		if h[i].Equal(slot) {
			h = append(h[:i:i], h[i+1:]...)
			break
		}
	}
	if len(h) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = h
	}
}

// add records one attempt.
func (l *attemptLimiter) add(key string, now time.Time) {
	l.reserve(key, now)
}

// allow records an attempt if key is not blocked.
func (l *attemptLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	ok, wait, _ := l.reserve(key, now)
	return ok, wait
}
