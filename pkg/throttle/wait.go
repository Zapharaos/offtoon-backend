package throttle

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// Wait blocks until it's safe to make a request according to throttling rules
// It applies both random delay and sliding window throttling
func (t *Throttler) Wait(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()

	// Check if we're currently blocked
	if !t.blockedUntil.IsZero() && now.Before(t.blockedUntil) {
		waitTime := t.blockedUntil.Sub(now)
		t.mu.Unlock()

		zap.L().Warn("Throttler: blocked by server",
			zap.String("client", t.name),
			zap.Duration("wait_time", waitTime),
			zap.Time("blocked_until", t.blockedUntil))

		select {
		case <-ctx.Done():
			t.mu.Lock()
			return ctx.Err()
		case <-time.After(waitTime):
		}

		t.mu.Lock()
		now = time.Now()
		// Clear block if time has passed
		if now.After(t.blockedUntil) {
			t.blockedUntil = time.Time{}
			t.status.IsBlocked = false
			zap.L().Info("Throttler: block period ended",
				zap.String("client", t.name))
		}
	}

	// Apply random delay since last request (base delay + adaptive delay)
	if !t.lastRequest.IsZero() && (t.config.DelayMinMs > 0 || t.config.DelayMaxMs > 0) {
		elapsed := now.Sub(t.lastRequest)

		// Calculate random delay within the range
		var baseDelay time.Duration
		if t.config.DelayMaxMs > t.config.DelayMinMs {
			// Random delay between min and max
			delayRange := t.config.DelayMaxMs - t.config.DelayMinMs
			randomDelay := t.config.DelayMinMs + (int(time.Now().UnixNano()) % (delayRange + 1))
			baseDelay = time.Duration(randomDelay) * time.Millisecond
		} else {
			// If max <= min, use min as fixed delay
			baseDelay = time.Duration(t.config.DelayMinMs) * time.Millisecond
		}

		// Add adaptive delay if enabled and active
		totalDelay := baseDelay
		if t.config.AdaptiveEnabled && t.adaptiveDelayMs > 0 {
			totalDelay += time.Duration(t.adaptiveDelayMs) * time.Millisecond
			zap.L().Debug("Throttler: applying adaptive delay",
				zap.String("client", t.name),
				zap.Duration("base_delay", baseDelay),
				zap.Duration("adaptive_delay", time.Duration(t.adaptiveDelayMs)*time.Millisecond),
				zap.Int("adaptation_level", t.status.AdaptationLevel))
		}

		if elapsed < totalDelay {
			waitTime := totalDelay - elapsed
			t.mu.Unlock()

			zap.L().Debug("Throttler: applying delay",
				zap.String("client", t.name),
				zap.Duration("wait_time", waitTime),
				zap.Duration("total_delay", totalDelay))

			select {
			case <-ctx.Done():
				t.mu.Lock()
				return ctx.Err()
			case <-time.After(waitTime):
			}

			t.mu.Lock()
			now = time.Now()
		}
	}

	// Clean up old requests outside the sliding window
	windowDuration := time.Duration(t.config.WindowSeconds) * time.Second
	cutoff := now.Add(-windowDuration)
	newLog := make([]time.Time, 0, len(t.requestLog))
	for _, reqTime := range t.requestLog {
		if reqTime.After(cutoff) {
			newLog = append(newLog, reqTime)
		}
	}
	t.requestLog = newLog

	// Check if we've exceeded the rate limit
	if len(t.requestLog) >= t.config.MaxRequests {
		// Need to wait until the oldest request falls outside the window
		oldestRequest := t.requestLog[0]
		waitUntil := oldestRequest.Add(windowDuration)
		waitTime := waitUntil.Sub(now)

		if waitTime > 0 {
			zap.L().Debug("Throttler: throttling - max requests reached",
				zap.String("client", t.name),
				zap.Int("max_requests", t.config.MaxRequests),
				zap.Duration("window", windowDuration),
				zap.Duration("wait_time", waitTime))

			t.mu.Unlock()

			select {
			case <-ctx.Done():
				t.mu.Lock()
				return ctx.Err()
			case <-time.After(waitTime):
			}

			t.mu.Lock()
			now = time.Now()
		}
	}

	// Record this request
	t.requestLog = append(t.requestLog, now)
	t.lastRequest = now

	return nil
}
