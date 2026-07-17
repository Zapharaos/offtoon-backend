package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/Zapharaos/offtoon-backend/internal/app"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// healthCacheTTL bounds how often the health endpoint actually probes its
// sources. A public, monitored endpoint may be polled frequently (uptime
// checks + a status page), so we serve a cached verdict in between to keep it
// cheap and to avoid hammering the source sites with probe requests.
const healthCacheTTL = 5 * time.Second

// healthProbeTimeout bounds a single source-reachability probe.
const healthProbeTimeout = 3 * time.Second

// healthProbeClient is a dedicated, short-timeout client for source probes so a
// slow/dead source can never hang the health endpoint for long.
var healthProbeClient = &http.Client{Timeout: healthProbeTimeout}

// componentHealth is the public, sanitized status of a single dependency.
// It intentionally carries no error details or internal addresses.
type componentHealth struct {
	Status string `json:"status"` // "ok" or "down"
}

// healthResponse is the public payload, stable enough to be consumed by an
// uptime monitor (e.g. Uptime Kuma) and rendered on a status page.
type healthResponse struct {
	Status        string                     `json:"status"` // always "ok" — see Health
	Version       string                     `json:"version,omitempty"`
	BuildDate     string                     `json:"buildDate,omitempty"`
	UptimeSeconds int64                      `json:"uptimeSeconds"`
	Timestamp     string                     `json:"timestamp"`
	Components    map[string]componentHealth `json:"components"`
}

var (
	healthMu       sync.Mutex
	healthCached   healthResponse
	healthCachedAt time.Time
)

// statusWord maps a boolean reachability to its public label.
func statusWord(ok bool) string {
	if ok {
		return "ok"
	}
	return "down"
}

// computeHealth builds the health payload. Each registered source is probed for
// reachability and reported as a component. Unlike a database-backed service,
// offtoon has no critical backing dependency: a source site being down only
// affects individual scrape requests, not the service's ability to run, so the
// overall status stays "ok" and a source outage is surfaced in Components only.
func (h *Handler) computeHealth() healthResponse {
	resp := healthResponse{
		Status:     "ok",
		Version:    app.Version(),
		BuildDate:  app.BuildDate(),
		Components: map[string]componentHealth{},
	}

	for _, name := range h.reg.Names() {
		resp.Components[name] = componentHealth{Status: statusWord(h.probeSource(name))}
	}

	return resp
}

// probeSource reports whether the source's first configured URL is reachable.
// Any HTTP response (even 4xx) counts as reachable — only a network/DNS/timeout
// error is treated as "down", since we are checking liveness, not correctness.
func (h *Handler) probeSource(name string) bool {
	urls := viper.GetStringSlice("clients." + name + ".urls")
	if len(urls) == 0 {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), healthProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, urls[0], nil)
	if err != nil {
		return false
	}

	resp, err := healthProbeClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// Health handles GET/HEAD /health
//
//	@Summary		Health check
//	@Description	Public health endpoint for uptime monitors (e.g. Uptime Kuma) and status pages. Always returns HTTP 200 while the service is running, with build/uptime metadata and per-source reachability under "components". Sources are non-critical: a source outage shows as {"status":"down"} in components but does not change the HTTP status, so the monitor tracks the service itself rather than upstream sites. Source probes are cached for a few seconds to stay cheap under frequent polling.
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	healthResponse	"Service is healthy"
//	@Router			/health [get]
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	healthMu.Lock()
	if healthCachedAt.IsZero() || time.Since(healthCachedAt) > healthCacheTTL {
		healthCached = h.computeHealth()
		healthCachedAt = time.Now()
	}
	resp := healthCached
	healthMu.Unlock()

	// Uptime and timestamp always reflect "now", even on a cached verdict.
	resp.Timestamp = time.Now().UTC().Format(time.RFC3339)
	resp.UptimeSeconds = int64(app.Uptime().Seconds())

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	// HEAD requests only need the status code and headers.
	if r.Method == http.MethodHead {
		return
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		zap.L().Error("Failed to encode health response", zap.Error(err))
	}
}
