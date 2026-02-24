// Package asura implements the api.Client interface for asuracomic.net.
package asura

import (
	"context"
	"fmt"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/throttle"
	"github.com/spf13/viper"
)

// Name is the unique identifier used for registration and routing.
const Name = "asura"

// -----------------------------------------------------------------------
// Client
// -----------------------------------------------------------------------

// Client implements api.Client for asuracomic.net.
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
// Search
// -----------------------------------------------------------------------

// FetchParams carries the data needed to identify a single toon on Asura.
type FetchParams struct {
	// Slug is the URL slug for the toon (e.g. "0a59965f-some-toon-slug").
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
// Interface implementation (stubs – real scraping logic to be added)
// -----------------------------------------------------------------------

// Search returns toons whose title matches query across all configured URLs.
func (c *Client) Search(_ context.Context, _ string) ([]toon.SearchResult, error) {
	// TODO: implement Asura search scraping
	return nil, toon.ErrNotFound
}

// SearchWithExtraURLs behaves like Search but prepends extraURLs to the
// client's configured URL list for this call only.
func (c *Client) SearchWithExtraURLs(ctx context.Context, query string, extraURLs []string) ([]toon.SearchResult, error) {
	// Swap the BaseClient for a one-shot clone with the extra URLs prepended,
	// then delegate to Search which uses c.TryURLs internally.
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
	// TODO: implement Asura toon detail scraping
	return nil, toon.ErrNotFound
}

// Download returns the chapters (with pages) for the given source-specific params.
func (c *Client) Download(_ context.Context, params api.DownloadParams) ([]toon.Chapter, error) {
	if _, ok := params.(DownloadParams); !ok {
		return nil, fmt.Errorf("%s: Download received wrong params type %T", Name, params)
	}
	// TODO: implement Asura chapter/page scraping
	return nil, toon.ErrNotFound
}
