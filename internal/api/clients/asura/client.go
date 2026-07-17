// Package asura implements the api.Client interface for asurascans.com
// via its public JSON API at api.asurascans.com.
package asura

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/throttle"
	"github.com/spf13/viper"
	"golang.org/x/net/html"
)

// Name is the unique identifier used for registration and routing.
const Name = "asura"

// publicBaseURL is the human-facing site used for SourceURL fields.
const publicBaseURL = "https://asurascans.com"

// hashSuffixRe matches old-style slug suffixes like "-cd494674" (8 hex chars).
var hashSuffixRe = regexp.MustCompile(`^(.+)-[0-9a-f]{8}$`)

// -----------------------------------------------------------------------
// Client
// -----------------------------------------------------------------------

// Client implements api.Client for asurascans.com.
type Client struct {
	*api.BaseClient
}

// New creates a ready-to-use Asura client whose URL list and throttle
// settings are taken from the application configuration.
func New() (*Client, error) {
	urls := viper.GetStringSlice("clients.asura.urls")
	if len(urls) == 0 {
		return nil, fmt.Errorf("asura: no URLs configured under clients.asura.urls")
	}

	return &Client{
		BaseClient: api.NewBaseClient(Name, urls, throttleConfig()),
	}, nil
}

// throttleConfig builds the throttle configuration from viper (clients.asura.throttle.*).
func throttleConfig() *throttle.Config {
	const prefix = "clients.asura.throttle."

	viper.SetDefault(prefix+"delay_min_ms", 800)
	viper.SetDefault(prefix+"delay_max_ms", 2000)
	viper.SetDefault(prefix+"max_requests", 8)
	viper.SetDefault(prefix+"window_seconds", 60)
	viper.SetDefault(prefix+"max_attempts", 3)
	viper.SetDefault(prefix+"initial_backoff_ms", 1000)
	viper.SetDefault(prefix+"max_backoff_ms", 30000)
	viper.SetDefault(prefix+"backoff_multiplier", 2.0)
	viper.SetDefault(prefix+"baseline_response_time_ms", 400)
	viper.SetDefault(prefix+"slow_threshold_multiplier", 3.0)
	viper.SetDefault(prefix+"adaptive_enabled", true)

	return &throttle.Config{
		DelayMinMs:              viper.GetInt(prefix + "delay_min_ms"),
		DelayMaxMs:              viper.GetInt(prefix + "delay_max_ms"),
		MaxRequests:             viper.GetInt(prefix + "max_requests"),
		WindowSeconds:           viper.GetInt(prefix + "window_seconds"),
		MaxAttempts:             viper.GetInt(prefix + "max_attempts"),
		InitialBackoffMs:        viper.GetInt(prefix + "initial_backoff_ms"),
		MaxBackoffMs:            viper.GetInt(prefix + "max_backoff_ms"),
		BackoffMultiplier:       viper.GetFloat64(prefix + "backoff_multiplier"),
		UserAgents:              throttle.GetDefaultUserAgents(),
		BaselineResponseTimeMs:  viper.GetInt(prefix + "baseline_response_time_ms"),
		SlowThresholdMultiplier: viper.GetFloat64(prefix + "slow_threshold_multiplier"),
		AdaptiveEnabled:         viper.GetBool(prefix + "adaptive_enabled"),
	}
}

// -----------------------------------------------------------------------
// Params
// -----------------------------------------------------------------------

// FetchParams carries the data needed to identify a single toon on Asura.
type FetchParams struct {
	// Slug is the URL slug for the toon (e.g. "the-dark-swordsman-returns").
	Slug string
}

func (p FetchParams) ClientName() string { return Name }

// DownloadParams carries the data needed to download chapters from Asura.
type DownloadParams struct {
	// Slug is the URL slug for the toon.
	Slug string
	// ChapterIDs limits the download to specific chapters; empty means all.
	ChapterIDs []string
}

func (p DownloadParams) ClientName() string { return Name }

// -----------------------------------------------------------------------
// JSON DTOs
// -----------------------------------------------------------------------

type apiGenre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type apiSeries struct {
	ID            int        `json:"id"`
	Slug          string     `json:"slug"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	Cover         string     `json:"cover"`
	Status        string     `json:"status"`
	Type          string     `json:"type"`
	Author        string     `json:"author"`
	Artist        string     `json:"artist"`
	Rating        float64    `json:"rating"`
	ChapterCount  int        `json:"chapter_count"`
	UpdatedAt     *time.Time `json:"updated_at"`
	LastChapterAt *time.Time `json:"last_chapter_at"`
	PublicURL     string     `json:"public_url"`
	Genres        []apiGenre `json:"genres"`
}

type apiSearchMeta struct {
	Total   int  `json:"total"`
	PerPage int  `json:"per_page"`
	HasMore bool `json:"has_more"`
}

type apiSearchResponse struct {
	Data []apiSeries   `json:"data"`
	Meta apiSearchMeta `json:"meta"`
}

type apiSeriesResponse struct {
	Series apiSeries `json:"series"`
}

type apiChapterMeta struct {
	ID          int        `json:"id"`
	Number      float64    `json:"number"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	IsPremium   bool       `json:"is_premium"`
	IsLocked    bool       `json:"is_locked"`
	PublishedAt *time.Time `json:"published_at"`
	SeriesSlug  string     `json:"series_slug"`
}

type apiChaptersResponse struct {
	Data []apiChapterMeta `json:"data"`
}

type apiPage struct {
	URL string `json:"url"`
}

type apiChapterPages struct {
	Data struct {
		AccessGate string `json:"access_gate"`
		Chapter    struct {
			ID     int       `json:"id"`
			Number float64   `json:"number"`
			Title  string    `json:"title"`
			Slug   string    `json:"slug"`
			Pages  []apiPage `json:"pages"`
		} `json:"chapter"`
	} `json:"data"`
}

// -----------------------------------------------------------------------
// Shared helpers
// -----------------------------------------------------------------------

// doJSON performs a GET request and JSON-decodes the response into out.
// Returns false (not found) when the server replies 404.
func (c *Client) doJSON(ctx context.Context, reqURL string, out any) (found bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return false, fmt.Errorf("%s: build request for %s: %w", Name, reqURL, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.Do(ctx, req)
	if err != nil {
		return false, fmt.Errorf("%s: GET %s: %w", Name, reqURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%s: unexpected status %d for %s", Name, resp.StatusCode, reqURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("%s: read body for %s: %w", Name, reqURL, err)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return false, fmt.Errorf("%s: decode JSON from %s: %w", Name, reqURL, err)
	}
	return true, nil
}

// doJSONOnce is like doJSON but uses DoOnce (no retry) — for requests where
// a non-200 is a permanent condition (e.g. stale slug).
func (c *Client) doJSONOnce(ctx context.Context, reqURL string, out any) (found bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return false, fmt.Errorf("%s: build request for %s: %w", Name, reqURL, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.DoOnce(ctx, req)
	if err != nil {
		return false, fmt.Errorf("%s: GET %s: %w", Name, reqURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%s: unexpected status %d for %s", Name, resp.StatusCode, reqURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("%s: read body for %s: %w", Name, reqURL, err)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return false, fmt.Errorf("%s: decode JSON from %s: %w", Name, reqURL, err)
	}
	return true, nil
}

// parseAPIStatus maps lowercase API status strings to toon.Status constants.
func parseAPIStatus(raw string) toon.Status {
	switch strings.ToLower(strings.ReplaceAll(raw, " ", "_")) {
	case "ongoing":
		return toon.StatusOngoing
	case "completed":
		return toon.StatusCompleted
	case "hiatus":
		return toon.StatusHiatus
	case "dropped":
		return toon.StatusDropped
	case "season_end":
		return toon.StatusSeasonEnd
	default:
		return toon.StatusUnknown
	}
}

// stripHTML extracts plain text from an HTML string.
func stripHTML(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return s
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return strings.TrimSpace(sb.String())
}

// stripHashSuffix removes a trailing 8-hex-char suffix from a slug if present.
// Old slugs stored by the UI may carry a hash suffix (e.g. "some-toon-cd494674").
func stripHashSuffix(slug string) (string, bool) {
	m := hashSuffixRe.FindStringSubmatch(slug)
	if m == nil {
		return slug, false
	}
	return m[1], true
}
