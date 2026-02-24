package throttle

import (
	"time"

	"go.uber.org/zap"
)

// recordResponseTime records a successful response time and triggers adaptation if needed
func (t *Throttler) recordResponseTime(responseTime time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Add response time to sliding window
	t.responseTimes = append(t.responseTimes, responseTime)

	// Keep only the most recent N response times
	if len(t.responseTimes) > t.maxResponseTimes {
		t.responseTimes = t.responseTimes[len(t.responseTimes)-t.maxResponseTimes:]
	}

	// Calculate average response time
	if len(t.responseTimes) > 0 {
		var total time.Duration
		for _, rt := range t.responseTimes {
			total += rt
		}
		t.status.AvgResponseTime = total / time.Duration(len(t.responseTimes))
	}

	// Check if adaptive throttling should be triggered
	if t.config.AdaptiveEnabled && len(t.responseTimes) >= 5 {
		t.adaptToResponseTime()
	}
}

// adaptToResponseTime adjusts throttling based on response time patterns
func (t *Throttler) adaptToResponseTime() {
	baseline := t.status.BaselineResponseTime
	current := t.status.AvgResponseTime
	slowThreshold := time.Duration(float64(baseline) * t.config.SlowThresholdMultiplier)

	// Determine adaptation level based on how slow responses are
	var newLevel int
	var newAdaptiveDelay int
	isSlowTraffic := false

	if current > slowThreshold {
		// Severe slowdown - responses are 3x+ baseline
		isSlowTraffic = true
		if current > slowThreshold*2 {
			// Extreme: 6x+ baseline
			newLevel = 3
			newAdaptiveDelay = t.config.DelayMaxMs * 2 // Double the max delay
		} else if current > time.Duration(float64(slowThreshold)*1.5) {
			// Severe: 4.5x+ baseline
			newLevel = 3
			newAdaptiveDelay = t.config.DelayMaxMs
		} else {
			// Moderate: 3x+ baseline
			newLevel = 2
			newAdaptiveDelay = t.config.DelayMaxMs / 2
		}
	} else if current > baseline*2 {
		// Slight slowdown - responses are 2x+ baseline
		isSlowTraffic = true
		newLevel = 1
		newAdaptiveDelay = t.config.DelayMaxMs / 4
	} else if current < time.Duration(float64(baseline)*1.2) {
		// Normal or better - gradually reduce adaptation
		isSlowTraffic = false
		newLevel = 0
		newAdaptiveDelay = 0
	} else {
		// Between 1.2x and 2x - maintain current level or reduce slowly
		if t.status.AdaptationLevel > 0 {
			newLevel = t.status.AdaptationLevel - 1
			newAdaptiveDelay = t.adaptiveDelayMs / 2
		} else {
			newLevel = 0
			newAdaptiveDelay = 0
		}
	}

	// Only update if there's a change
	if newLevel != t.status.AdaptationLevel || isSlowTraffic != t.status.IsSlowTraffic {
		oldLevel := t.status.AdaptationLevel
		oldDelay := t.adaptiveDelayMs

		t.status.AdaptationLevel = newLevel
		t.status.IsSlowTraffic = isSlowTraffic
		t.adaptiveDelayMs = newAdaptiveDelay
		t.status.LastAdaptationTime = time.Now()

		zap.L().Info("Throttler: adapted to response time",
			zap.String("client", t.name),
			zap.Duration("baseline", baseline),
			zap.Duration("current_avg", current),
			zap.Duration("slow_threshold", slowThreshold),
			zap.Int("old_level", oldLevel),
			zap.Int("new_level", newLevel),
			zap.Int("old_adaptive_delay_ms", oldDelay),
			zap.Int("new_adaptive_delay_ms", newAdaptiveDelay),
			zap.Bool("is_slow_traffic", isSlowTraffic))
	}
}

// markAsBlocked marks the throttler as blocked by the server
// This is called when a 429 error is received, indicating our throttling was insufficient
func (t *Throttler) markAsBlocked(retryAfter time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Default block duration if not specified
	if retryAfter <= 0 {
		retryAfter = time.Duration(t.config.InitialBackoffMs*2) * time.Millisecond
	}

	t.blockedUntil = time.Now().Add(retryAfter)
	t.status.IsBlocked = true
	t.status.ThrottleEndsAt = t.blockedUntil

	// Increase adaptation level when blocked
	if t.config.AdaptiveEnabled {
		t.status.AdaptationLevel = 3 // Maximum adaptation
		t.adaptiveDelayMs = t.config.DelayMaxMs * 2
		t.status.IsSlowTraffic = true
		t.status.LastAdaptationTime = time.Now()

		zap.L().Error("Throttler: marked as blocked due to 429 - increasing to maximum adaptation",
			zap.String("client", t.name),
			zap.Duration("blocked_for", retryAfter),
			zap.Time("blocked_until", t.blockedUntil),
			zap.Int("adaptation_level", t.status.AdaptationLevel),
			zap.Int("adaptive_delay_ms", t.adaptiveDelayMs),
			zap.String("note", "This means our throttling barriers were insufficient"))
	}
}
