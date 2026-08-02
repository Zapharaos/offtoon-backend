package archiver

import (
	"context"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/Zapharaos/offtoon-backend/pkg/throttle"
)

// imageUserAgents is the pool of realistic browser User-Agent strings used for
// image requests, shared with the source API clients (see pkg/throttle).
var imageUserAgents = throttle.GetDefaultUserAgents()

// setBrowserImageHeaders makes an image request look like one issued by a browser
// loading an <img> tag.
//
// The User-Agent is the header that matters most: Go's http.Client defaults to
// "Go-http-client/1.1", which the CDN edge (Cloudflare, in front of Asura)
// fingerprints as a bot and rate-limits aggressively no matter how slowly the
// requests are paced. The API clients already rotate a realistic agent via
// throttle.SetRequestUserAgent — image downloads must do the same or they get
// throttled while ordinary browsing succeeds.
//
// referer is the chapter URL, required by CDNs enforcing hotlink protection.
// Accept-Encoding is deliberately left unset so net/http keeps handling gzip
// transparently.
func setBrowserImageHeaders(req *http.Request, referer string) {
	if len(imageUserAgents) > 0 {
		req.Header.Set("User-Agent", imageUserAgents[rand.Intn(len(imageUserAgents))])
	}
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	req.Header.Set("Sec-Fetch-Dest", "image")
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
}

// imageRequestInterval is the minimum spacing between two image requests, across
// every chapter and every worker. At 250ms the archiver sends at most ~4 requests
// per second, which stays under the thresholds enforced by the CDNs we fetch from.
//
// This is the single knob controlling download pressure. Worker counts only decide
// how many requests may be in flight at once; this interval decides how fast new
// ones are allowed to start. That distinction matters because the archiver nests
// two worker pools (chapters × images) — without a shared throttle the effective
// request rate is the product of the two pool sizes, which is what triggered
// sustained 429s from Asura's edge.
const imageRequestInterval = 250 * time.Millisecond

// cdnThrottle paces every outbound image request and implements a shared cooldown
// so a 429 observed by one worker pauses all of them.
//
// The shared cooldown is the important half: with per-request backoff only, the
// goroutine that received the 429 would sleep while its peers kept hammering the
// CDN, which keeps the rate limit engaged instead of letting it expire.
//
// The zero value is not usable; call newCDNThrottle.
type cdnThrottle struct {
	interval time.Duration

	mu            sync.Mutex
	next          time.Time // earliest instant the next request may start
	cooldownUntil time.Time // global pause requested by a 429 response
}

func newCDNThrottle(interval time.Duration) *cdnThrottle {
	return &cdnThrottle{interval: interval}
}

// acquire blocks until the caller may issue its request, or until ctx is
// cancelled. Callers reserve the next free slot, so concurrent workers are
// serialised into an evenly spaced stream instead of arriving in bursts.
func (t *cdnThrottle) acquire(ctx context.Context) error {
	for {
		if err := sleepUntil(ctx, t.reserve()); err != nil {
			return err
		}

		// A 429 may have landed while we waited on our slot. If so, drop this
		// reservation and take a new one past the cooldown. This terminates
		// because every cooldown is finite.
		t.mu.Lock()
		blocked := t.cooldownUntil.After(time.Now())
		t.mu.Unlock()
		if !blocked {
			return nil
		}
	}
}

// reserve claims the next request slot and returns the instant it opens.
func (t *cdnThrottle) reserve() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()

	slot := t.next
	if t.cooldownUntil.After(slot) {
		slot = t.cooldownUntil
	}
	if now := time.Now(); slot.Before(now) {
		slot = now
	}
	t.next = slot.Add(t.interval)
	return slot
}

// penalize records a rate-limit response: no request may start for d. Concurrent
// 429s keep the longest cooldown rather than shortening an existing one.
func (t *cdnThrottle) penalize(d time.Duration) {
	if d <= 0 {
		return
	}
	until := time.Now().Add(d)

	t.mu.Lock()
	defer t.mu.Unlock()

	if until.After(t.cooldownUntil) {
		t.cooldownUntil = until
	}
	// Push the pacing cursor past the cooldown so the requests queued behind it
	// stay spaced out instead of all firing the instant it expires.
	if t.cooldownUntil.After(t.next) {
		t.next = t.cooldownUntil
	}
}

// sleepUntil waits for the given instant, returning early if ctx is cancelled.
func sleepUntil(ctx context.Context, deadline time.Time) error {
	d := time.Until(deadline)
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
