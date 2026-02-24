package throttle

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Config holds the configuration for throttling and retry logic
type Config struct {
	// Random delay range between requests (in milliseconds)
	DelayMinMs int
	DelayMaxMs int

	// Throttle configuration (sliding window)
	MaxRequests   int
	WindowSeconds int

	// Retry configuration
	MaxAttempts       int
	InitialBackoffMs  int
	MaxBackoffMs      int
	BackoffMultiplier float64

	// User agent rotation
	UserAgents []string

	// Adaptive throttling thresholds
	BaselineResponseTimeMs  int     // Expected normal response time (ms)
	SlowThresholdMultiplier float64 // Multiplier for slow detection (e.g., 3.0 = 3x baseline)
	AdaptiveEnabled         bool    // Enable adaptive throttling
}

// Status represents the current state of the throttler
type Status struct {
	IsBlocked            bool          // Currently blocked by server (429 or rate limit error)
	IsSlowTraffic        bool          // Traffic is slower than normal (server stressed)
	ThrottleEndsAt       time.Time     // When current throttle period ends
	AvgResponseTime      time.Duration // Recent average response time
	BaselineResponseTime time.Duration // Expected baseline response time
	AdaptationLevel      int           // 0=normal, 1=slight slowdown, 2=moderate, 3=severe
	LastAdaptationTime   time.Time     // When last adaptation occurred
}

// Throttler implements rate limiting, throttling, and retry with exponential backoff
type Throttler struct {
	config      Config
	mu          sync.Mutex
	requestLog  []time.Time
	lastRequest time.Time
	name        string // For logging purposes

	// Response time tracking for adaptive throttling
	responseTimes    []time.Duration // Recent response times (sliding window)
	maxResponseTimes int             // Maximum number of response times to track

	// Current status
	status          Status
	blockedUntil    time.Time // When the block expires
	adaptiveDelayMs int       // Additional delay due to adaptation (added to base delay)
}

// New creates a new Throttler instance
func New(name string, config Config) *Throttler {
	// Set defaults for adaptive throttling if not configured
	if config.BaselineResponseTimeMs <= 0 {
		config.BaselineResponseTimeMs = 200 // Default: 200ms
	}
	if config.SlowThresholdMultiplier <= 0 {
		config.SlowThresholdMultiplier = 3.0 // Default: 3x baseline
	}

	return &Throttler{
		config:           config,
		requestLog:       make([]time.Time, 0, config.MaxRequests),
		name:             name,
		responseTimes:    make([]time.Duration, 0, 20), // Track last 20 response times
		maxResponseTimes: 20,
		status: Status{
			BaselineResponseTime: time.Duration(config.BaselineResponseTimeMs) * time.Millisecond,
		},
	}
}

// GetStatus returns the current status of the throttler
func (t *Throttler) GetStatus() Status {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Update ThrottleEndsAt if we're currently blocked
	if !t.blockedUntil.IsZero() && time.Now().Before(t.blockedUntil) {
		t.status.ThrottleEndsAt = t.blockedUntil
	} else {
		t.status.ThrottleEndsAt = time.Time{}
	}

	return t.status
}

// GetStats returns current throttler statistics
func (t *Throttler) GetStats() map[string]interface{} {
	t.mu.Lock()
	defer t.mu.Unlock()

	windowDuration := time.Duration(t.config.WindowSeconds) * time.Second
	cutoff := time.Now().Add(-windowDuration)

	activeRequests := 0
	for _, reqTime := range t.requestLog {
		if reqTime.After(cutoff) {
			activeRequests++
		}
	}

	stats := map[string]interface{}{
		"client":          t.name,
		"max_requests":    t.config.MaxRequests,
		"window_seconds":  t.config.WindowSeconds,
		"active_requests": activeRequests,
		"available_slots": int(math.Max(0, float64(t.config.MaxRequests-activeRequests))),
		"last_request":    t.lastRequest,

		// Status information
		"is_blocked":       t.status.IsBlocked,
		"is_slow_traffic":  t.status.IsSlowTraffic,
		"adaptation_level": t.status.AdaptationLevel,

		// Response time monitoring
		"avg_response_time_ms":      t.status.AvgResponseTime.Milliseconds(),
		"baseline_response_time_ms": t.status.BaselineResponseTime.Milliseconds(),
		"response_samples":          len(t.responseTimes),

		// Adaptive delays
		"base_delay_range_ms": fmt.Sprintf("%d-%d", t.config.DelayMinMs, t.config.DelayMaxMs),
		"adaptive_delay_ms":   t.adaptiveDelayMs,
		"adaptive_enabled":    t.config.AdaptiveEnabled,
	}

	// Add timing information
	if !t.blockedUntil.IsZero() && time.Now().Before(t.blockedUntil) {
		stats["blocked_until"] = t.blockedUntil
		stats["blocked_for_seconds"] = time.Until(t.blockedUntil).Seconds()
	}

	if !t.status.ThrottleEndsAt.IsZero() {
		stats["throttle_ends_at"] = t.status.ThrottleEndsAt
	}

	if !t.status.LastAdaptationTime.IsZero() {
		stats["last_adaptation"] = t.status.LastAdaptationTime
		stats["seconds_since_adaptation"] = time.Since(t.status.LastAdaptationTime).Seconds()
	}

	return stats
}

// ResetAdaptation resets the adaptive throttling to normal levels
func (t *Throttler) ResetAdaptation() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.status.AdaptationLevel = 0
	t.status.IsSlowTraffic = false
	t.adaptiveDelayMs = 0
	t.responseTimes = make([]time.Duration, 0, t.maxResponseTimes)

	zap.L().Info("Throttler: adaptation reset to normal",
		zap.String("client", t.name))
}

// parseRetryAfter parses the Retry-After header from HTTP response
// Returns 0 if header is not present or cannot be parsed
func parseRetryAfter(resp *http.Response) time.Duration {
	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		return 0
	}

	// Try to parse as seconds (integer)
	if seconds, err := strconv.Atoi(retryAfter); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// Try to parse as HTTP date
	if t, err := time.Parse(time.RFC1123, retryAfter); err == nil {
		duration := time.Until(t)
		if duration > 0 {
			return duration
		}
	}

	return 0
}

// LogRateLimitHeaders logs rate limit information from response headers
func (t *Throttler) LogRateLimitHeaders(resp *http.Response) {
	remaining := resp.Header.Get("X-RateLimit-Remaining")
	reset := resp.Header.Get("X-RateLimit-Reset")
	limit := resp.Header.Get("X-RateLimit-Limit")

	if remaining != "" || reset != "" || limit != "" {
		fields := []zap.Field{
			zap.String("client", t.name),
		}

		if limit != "" {
			fields = append(fields, zap.String("rate_limit", limit))
		}
		if remaining != "" {
			fields = append(fields, zap.String("remaining", remaining))
		}
		if reset != "" {
			// Try to parse as timestamp
			if resetInt, err := strconv.ParseInt(reset, 10, 64); err == nil {
				resetTime := time.Unix(resetInt, 0)
				fields = append(fields, zap.Time("reset_at", resetTime))
			} else {
				fields = append(fields, zap.String("reset", reset))
			}
		}

		zap.L().Debug("Throttler: rate limit headers from API", fields...)
	}
}
