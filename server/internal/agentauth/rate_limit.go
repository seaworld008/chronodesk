package agentauth

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type anonymousRateWindow struct {
	start time.Time
	count int
}

// anonymousLimiter protects the unauthenticated token exchange independently
// from application-wide limits. Keys are bounded by periodic expiry pruning.
type anonymousLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	now     func() time.Time
	windows map[string]anonymousRateWindow
}

func newAnonymousLimiter(limit int, window time.Duration) *anonymousLimiter {
	if limit <= 0 {
		limit = 30
	}
	if window <= 0 {
		window = time.Minute
	}
	return &anonymousLimiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		windows: make(map[string]anonymousRateWindow),
	}
}

func (l *anonymousLimiter) allow(key string) (bool, time.Duration) {
	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()

	current, exists := l.windows[key]
	if !exists || !now.Before(current.start.Add(l.window)) {
		l.windows[key] = anonymousRateWindow{start: now, count: 1}
		l.pruneExpired(now)
		return true, 0
	}
	if current.count >= l.limit {
		return false, current.start.Add(l.window).Sub(now)
	}
	current.count++
	l.windows[key] = current
	return true, 0
}

func (l *anonymousLimiter) pruneExpired(now time.Time) {
	// Avoid a scan on every request while still bounding keys created by
	// short-lived source addresses.
	if len(l.windows) < 1024 {
		return
	}
	for key, window := range l.windows {
		if !now.Before(window.start.Add(l.window)) {
			delete(l.windows, key)
		}
	}
}

func (h *Handler) limitTokenRequests() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.tokenLimiter == nil {
			writeOAuthError(c, http.StatusServiceUnavailable, "temporarily_unavailable", "Agent authorization is not available")
			c.Abort()
			return
		}
		key := c.ClientIP() + "|" + c.FullPath()
		allowed, retryAfter := h.tokenLimiter.allow(key)
		if !allowed {
			seconds := int(retryAfter.Round(time.Second).Seconds())
			if seconds < 1 {
				seconds = 1
			}
			c.Header("Retry-After", strconv.Itoa(seconds))
			writeOAuthError(c, http.StatusTooManyRequests, "temporarily_unavailable", "Too many token requests")
			c.Abort()
			return
		}
		c.Next()
	}
}
