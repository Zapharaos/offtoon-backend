// Package nato implements the api.Client interface for natomanga.com and its mirrors.
package nato

import (
	"context"
	"fmt"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/throttle"
	"github.com/spf13/viper"
)

// Name is the unique identifier used for registration and routing.
const Name = "nato"

// -----------------------------------------------------------------------
// Client
// -----------------------------------------------------------------------

// Client implements api.Client for natomanga.com and its mirrors.
// URLs are tried in the order listed in config; the first one that
// returns usable data wins (URL-fallback via BaseClient.TryURLs).
type Client struct {
	*api.BaseClient
}

// New creates a ready-to-use Nato client whose URL list and throttle
// settings are taken from the application configuration.
func New() (*Client, error) {
	urls := viper.GetStringSlice("clients.nato.urls")
	if len(urls) == 0 {
		return nil, fmt.Errorf("nato: no URLs configured under clients.nato.urls")
	}

	return &Client{
		BaseClient: api.NewBaseClient(Name, urls, throttleConfig()),
	}, nil
}

// throttleConfig builds the throttle configuration from viper (clients.nato.throttle.*).
func throttleConfig() *throttle.Config {
	const prefix = "clients.nato.throttle."

	viper.SetDefault(prefix+"delay_min_ms", 600)
	viper.SetDefault(prefix+"delay_max_ms", 1800)
	viper.SetDefault(prefix+"max_requests", 10)
	viper.SetDefault(prefix+"window_seconds", 60)
	viper.SetDefault(prefix+"max_attempts", 3)
	viper.SetDefault(prefix+"initial_backoff_ms", 1000)
	viper.SetDefault(prefix+"max_backoff_ms", 30000)
	viper.SetDefault(prefix+"backoff_multiplier", 2.0)
	viper.SetDefault(prefix+"baseline_response_time_ms", 350)
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
// Per-operation parameter structs
// -----------------------------------------------------------------------

// FetchParams carries the data needed to identify a single toon on Nato.
type FetchParams struct {
	// Slug is the URL slug for the toon (e.g. "manga-ax123456").
	Slug string
}

func (p FetchParams) ClientName() string { return Name }

// DownloadParams carries the data needed to download chapters from Nato.
type DownloadParams struct {
	// Slug is the URL slug for the toon.
	Slug string
	// ChapterIDs limits the download to specific chapters; empty means all.
	ChapterIDs []string
	// Language filters chapters by language code (e.g. "en"). Empty means all.
	Language string
}

func (p DownloadParams) ClientName() string { return Name }

// -----------------------------------------------------------------------
// Interface implementation (stubs – real scraping logic to be added)
// -----------------------------------------------------------------------

// Search returns toons whose title matches query across all configured URLs.
func (c *Client) Search(_ context.Context, _ string) ([]toon.SearchResult, error) {
	// TODO: implement Nato search scraping
	return nil, toon.ErrNotFound
}

// SearchWithExtraURLs behaves like Search but prepends extraURLs to the
// client's configured URL list for this call only.
func (c *Client) SearchWithExtraURLs(ctx context.Context, query string, extraURLs []string) ([]toon.SearchResult, error) {
	original := c.BaseClient
	c.BaseClient = c.BaseClient.WithExtraURLs(extraURLs)
	defer func() { c.BaseClient = original }()
	return c.Search(ctx, query)
}

// Fetch returns the full Toon details for the given source-specific params.
func (c *Client) Fetch(_ context.Context, params api.FetchParams) (*toon.Toon, error) {
	if _, ok := params.(FetchParams); !ok {
		return nil, fmt.Errorf("%s: Fetch received wrong params type %T", Name, params)
	}
	// TODO: implement Nato toon detail scraping
	return nil, toon.ErrNotFound
}

// FetchWithExtraURLs behaves like Fetch but prepends extraURLs to the
// client's configured URL list for this call only.
func (c *Client) FetchWithExtraURLs(ctx context.Context, slug string, extraURLs []string) (*toon.Toon, error) {
	original := c.BaseClient
	c.BaseClient = c.BaseClient.WithExtraURLs(extraURLs)
	defer func() { c.BaseClient = original }()
	return c.Fetch(ctx, FetchParams{Slug: slug})
}

// Download returns the chapters (with pages) for the given source-specific params.
func (c *Client) Download(_ context.Context, params api.DownloadParams) ([]toon.Chapter, error) {
	if _, ok := params.(DownloadParams); !ok {
		return nil, fmt.Errorf("%s: Download received wrong params type %T", Name, params)
	}
	// TODO: implement Nato chapter/page scraping
	return nil, toon.ErrNotFound
}

// DownloadWithExtraURLs behaves like Download but prepends extraURLs to the
// client's configured URL list for this call only.
func (c *Client) DownloadWithExtraURLs(ctx context.Context, slug string, chapterIDs []string, extraURLs []string) ([]toon.Chapter, error) {
	original := c.BaseClient
	c.BaseClient = c.BaseClient.WithExtraURLs(extraURLs)
	defer func() { c.BaseClient = original }()
	return c.Download(ctx, DownloadParams{Slug: slug, ChapterIDs: chapterIDs})
}
