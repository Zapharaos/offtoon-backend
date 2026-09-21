package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/throttle"
	"go.uber.org/zap"
)

// -----------------------------------------------------------------------
// BaseClient
// -----------------------------------------------------------------------

// BaseClient is embedded by every concrete API client.
// It holds the URL list and a per-source Throttler so that all outgoing
// HTTP traffic is automatically rate-limited and retried.
type BaseClient struct {
	name      string
	urls      []string
	http      *http.Client
	throttler *throttle.Throttler
}

// NewBaseClient builds a BaseClient.  cfg is forwarded to the Throttler.
// If cfg is nil a safe default configuration is used.
func NewBaseClient(name string, urls []string, cfg *throttle.Config) *BaseClient {
	if cfg == nil {
		cfg = defaultThrottleConfig()
	}
	return &BaseClient{
		name:      name,
		urls:      urls,
		http:      &http.Client{Timeout: requestTimeout},
		throttler: throttle.New(name, *cfg),
	}
}

// requestTimeout bounds a single source request end to end, headers and body.
//
// Without it a source that stops answering mid-connection does not fail — it
// stalls until the OS gives up on the socket, which on Windows is ~21s per
// attempt and is multiplied by the retry loop and the URL-fallback loop. That
// turns one unresponsive host into minutes of apparent hanging. Every response
// we read is a JSON document or an HTML page, so 30s is far above what a
// healthy request needs while still failing fast on a dead one.
const requestTimeout = 30 * time.Second

// Name returns the source identifier.
func (b *BaseClient) Name() string { return b.name }

// URLs returns the ordered list of base URLs.
func (b *BaseClient) URLs() []string { return b.urls }

// Throttler exposes the underlying rate-limiter (useful for stats / reset).
func (b *BaseClient) Throttler() *throttle.Throttler { return b.throttler }

// -----------------------------------------------------------------------
// URL-fallback helpers
// -----------------------------------------------------------------------

// TryURLs iterates over every configured URL and calls fn(baseURL).
//
// Semantics
//   - fn returning (true, nil) → success, stop iterating, return the value.
//   - fn returning (false, nil) → "no result on this URL", try the next one.
//   - fn returning (_, err) → real error, stop and propagate.
//
// When fn returns (false, nil) for every URL, TryURLs returns
// toon.ErrNotFound so the caller never has to think about the distinction.
func (b *BaseClient) TryURLs(ctx context.Context, fn func(baseURL string) (ok bool, err error)) error {
	if len(b.urls) == 0 {
		return fmt.Errorf("%s: no URLs configured", b.name)
	}

	var lastErr error
	for _, u := range b.urls {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		ok, err := fn(u)
		if err != nil {
			// Real failure on this URL – log and keep trying the others.
			lastErr = err
			zap.L().Warn("API client URL attempt failed",
				zap.String("client", b.name),
				zap.String("url", u),
				zap.Error(err),
			)
			continue
		}
		if ok {
			return nil // success
		}
		// ok == false, err == nil → no data on this URL, move on silently
	}

	if lastErr != nil {
		return fmt.Errorf("%s: all URLs failed, last error: %w", b.name, lastErr)
	}
	// Every URL returned "no data" with no error → not found
	return toon.ErrNotFound
}

// -----------------------------------------------------------------------
// HTTP helper
// -----------------------------------------------------------------------

// Do sends req through the shared HTTP client, applying throttling and
// automatic retry.  The caller is responsible for closing resp.Body.
func (b *BaseClient) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	b.throttler.SetRequestUserAgent(req)
	resp, err := b.throttler.DoWithRetry(ctx, b.http, req)
	if err != nil {
		return nil, err
	}
	b.throttler.LogRateLimitHeaders(resp)
	return resp, nil
}

// DoOnce sends req through the shared HTTP client with throttle delay and
// user-agent rotation, but without automatic retry on 5xx. Use this for
// content pages where a 5xx is a permanent condition (e.g. stale slug),
// not a transient server error. The caller is responsible for closing resp.Body.
func (b *BaseClient) DoOnce(ctx context.Context, req *http.Request) (*http.Response, error) {
	b.throttler.SetRequestUserAgent(req)
	if err := b.throttler.Wait(ctx); err != nil {
		return nil, fmt.Errorf("throttler wait: %w", err)
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, err
	}
	b.throttler.LogRateLimitHeaders(resp)
	return resp, nil
}

// IsNotFound reports whether err signals "no result" as opposed to a real failure.
func IsNotFound(err error) bool {
	return errors.Is(err, toon.ErrNotFound)
}

// -----------------------------------------------------------------------
// Default throttle configuration
// -----------------------------------------------------------------------

func defaultThrottleConfig() *throttle.Config {
	return &throttle.Config{
		DelayMinMs:              500,
		DelayMaxMs:              1500,
		MaxRequests:             10,
		WindowSeconds:           60,
		MaxAttempts:             3,
		InitialBackoffMs:        1000,
		MaxBackoffMs:            30000,
		BackoffMultiplier:       2.0,
		UserAgents:              throttle.GetDefaultUserAgents(),
		BaselineResponseTimeMs:  300,
		SlowThresholdMultiplier: 3.0,
		AdaptiveEnabled:         true,
	}
}
