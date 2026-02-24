package throttle

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// DoWithRetry executes an HTTP request with automatic retry and exponential backoff
// This method ensures 429 errors are properly caught, logged, and handled with appropriate backoff
func (t *Throttler) DoWithRetry(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	var lastErr error
	backoff := time.Duration(t.config.InitialBackoffMs) * time.Millisecond
	maxBackoff := time.Duration(t.config.MaxBackoffMs) * time.Millisecond

	for attempt := 0; attempt < t.config.MaxAttempts; attempt++ {
		// Wait according to throttling rules
		if err := t.Wait(ctx); err != nil {
			return nil, fmt.Errorf("throttler wait cancelled: %w", err)
		}

		// Clone the request body if needed (for retries)
		var reqClone *http.Request
		if req.Body != nil && req.GetBody != nil {
			reqClone = req.Clone(ctx)
		} else {
			reqClone = req.WithContext(ctx)
		}

		// Track request start time for response time monitoring
		startTime := time.Now()

		// Execute the request
		resp, err := client.Do(reqClone)

		// Calculate response time
		responseTime := time.Since(startTime)

		// Success - record response time and check for adaptation
		if err == nil && resp.StatusCode < 500 && resp.StatusCode != 429 {
			t.recordResponseTime(responseTime)

			if attempt > 0 {
				zap.L().Info("Request succeeded after retry",
					zap.String("client", t.name),
					zap.Int("attempt", attempt+1),
					zap.Int("status_code", resp.StatusCode),
					zap.Duration("response_time", responseTime))
			}
			return resp, nil
		}

		// Handle errors and retryable status codes
		isLastAttempt := attempt == t.config.MaxAttempts-1

		if err != nil {
			lastErr = err
			zap.L().Warn("Request failed - network error",
				zap.String("client", t.name),
				zap.Int("attempt", attempt+1),
				zap.Int("max_attempts", t.config.MaxAttempts),
				zap.Error(err))
		} else {
			// Handle 429 (Too Many Requests) - THIS IS CRITICAL
			if resp.StatusCode == 429 {
				// 429 means our throttling barriers were insufficient
				// This is caught here and properly handled with block detection
				retryAfter := parseRetryAfter(resp)
				t.markAsBlocked(retryAfter)

				lastErr = fmt.Errorf("API returned status 429 (rate limited)")

				// Log detailed information about the 429 error
				zap.L().Error("❌ 429 Rate Limit Error - Our throttling barriers were bypassed",
					zap.String("client", t.name),
					zap.Int("attempt", attempt+1),
					zap.Int("status_code", 429),
					zap.Duration("retry_after", retryAfter),
					zap.Time("blocked_until", t.blockedUntil),
					zap.String("message", "The server rate limited us despite our throttling. Adjusting to maximum adaptation."))

				// Close response body before retry
				_ = resp.Body.Close()

				if !isLastAttempt {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(retryAfter):
					}
					continue
				}
			} else {
				// Handle 5xx errors
				lastErr = fmt.Errorf("API returned status %d", resp.StatusCode)

				zap.L().Warn("Request failed - server error",
					zap.String("client", t.name),
					zap.Int("attempt", attempt+1),
					zap.Int("max_attempts", t.config.MaxAttempts),
					zap.Int("status_code", resp.StatusCode))

				// Close response body before retry
				_ = resp.Body.Close()
			}
		}

		// Don't backoff on last attempt
		if isLastAttempt {
			break
		}

		// Apply exponential backoff
		zap.L().Debug("Applying exponential backoff",
			zap.String("client", t.name),
			zap.Int("attempt", attempt+1),
			zap.Duration("backoff", backoff))

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}

		// Calculate next backoff with exponential growth
		backoff = time.Duration(float64(backoff) * t.config.BackoffMultiplier)
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	return nil, fmt.Errorf("request failed after %d attempts: %w", t.config.MaxAttempts, lastErr)
}
