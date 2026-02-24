// Package example shows how to implement the api.Client interface for a
// concrete source.  Delete or replace this file once a real client exists.
package example

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/throttle"
)

// -----------------------------------------------------------------------
// Client-specific parameter structs
// -----------------------------------------------------------------------

// FetchParams carries the data needed to fetch a single toon from Example.
// Fields are source-specific; the only contract is ClientName().
type FetchParams struct {
	Slug string // e.g. "my-awesome-toon"
}

func (p FetchParams) ClientName() string { return ClientName }

// DownloadParams carries the data needed to download chapters from Example.
type DownloadParams struct {
	ToonSlug   string
	ChapterIDs []string // empty = all chapters
	Language   string   // e.g. "en"
}

func (p DownloadParams) ClientName() string { return ClientName }

// -----------------------------------------------------------------------
// Client
// -----------------------------------------------------------------------

const ClientName = "example"

// Client implements api.Client for the (fictional) Example source.
type Client struct {
	*api.BaseClient
}

// New creates a ready-to-use Example client.
// urls must contain at least one entry; they are tried in order.
// cfg may be nil to use api.DefaultThrottleConfig.
func New(urls []string, cfg *throttle.Config) *Client {
	return &Client{
		BaseClient: api.NewBaseClient(ClientName, urls, cfg),
	}
}

// -----------------------------------------------------------------------
// Search
// -----------------------------------------------------------------------

func (c *Client) Search(ctx context.Context, query string) ([]toon.SearchResult, error) {
	var results []toon.SearchResult

	err := c.TryURLs(ctx, func(baseURL string) (bool, error) {
		// Build the request – replace with real endpoint.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("%s/api/search?q=%s", baseURL, query), nil)
		if err != nil {
			return false, err
		}

		resp, err := c.Do(ctx, req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return false, nil // no results on this URL, try next
		}
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("unexpected status %d", resp.StatusCode)
		}

		// Decode the source-specific JSON into internal structs…
		var raw []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			CoverURL string `json:"cover_url"`
			URL      string `json:"url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return false, err
		}

		if len(raw) == 0 {
			return false, nil // empty page, try next URL
		}

		// …then map to the shared toon.SearchResult type.
		for _, item := range raw {
			results = append(results, toon.SearchResult{
				ID:        item.ID,
				Title:     item.Title,
				CoverURL:  item.CoverURL,
				Source:    ClientName,
				SourceURL: item.URL,
			})
		}
		return true, nil
	})

	if errors.Is(err, toon.ErrNotFound) {
		return nil, toon.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return results, nil
}

// -----------------------------------------------------------------------
// Fetch
// -----------------------------------------------------------------------

func (c *Client) Fetch(ctx context.Context, params api.FetchParams) (*toon.Toon, error) {
	p, ok := params.(FetchParams)
	if !ok {
		return nil, fmt.Errorf("%s: Fetch received wrong params type %T", ClientName, params)
	}

	var result *toon.Toon

	err := c.TryURLs(ctx, func(baseURL string) (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("%s/api/toon/%s", baseURL, p.Slug), nil)
		if err != nil {
			return false, err
		}

		resp, err := c.Do(ctx, req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("unexpected status %d", resp.StatusCode)
		}

		// Decode source-specific payload…
		var raw struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Author      string `json:"author"`
			Description string `json:"description"`
			CoverURL    string `json:"cover_url"`
			URL         string `json:"url"`
			Chapters    []struct {
				ID     string  `json:"id"`
				Title  string  `json:"title"`
				Number float64 `json:"number"`
				URL    string  `json:"url"`
			} `json:"chapters"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return false, err
		}

		// …map to the shared toon.Toon type.
		chapters := make([]toon.Chapter, len(raw.Chapters))
		for i, ch := range raw.Chapters {
			chapters[i] = toon.Chapter{
				ID:     ch.ID,
				Title:  ch.Title,
				Number: ch.Number,
				URL:    ch.URL,
			}
		}

		result = &toon.Toon{
			ID:          raw.ID,
			Title:       raw.Title,
			Author:      raw.Author,
			Description: raw.Description,
			CoverURL:    raw.CoverURL,
			Source:      ClientName,
			SourceURL:   raw.URL,
			Chapters:    chapters,
		}
		return true, nil
	})

	if errors.Is(err, toon.ErrNotFound) {
		return nil, toon.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// -----------------------------------------------------------------------
// Download
// -----------------------------------------------------------------------

func (c *Client) Download(ctx context.Context, params api.DownloadParams) ([]toon.Chapter, error) {
	p, ok := params.(DownloadParams)
	if !ok {
		return nil, fmt.Errorf("%s: Download received wrong params type %T", ClientName, params)
	}

	var chapters []toon.Chapter

	err := c.TryURLs(ctx, func(baseURL string) (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("%s/api/toon/%s/chapters?lang=%s", baseURL, p.ToonSlug, p.Language), nil)
		if err != nil {
			return false, err
		}

		resp, err := c.Do(ctx, req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("unexpected status %d", resp.StatusCode)
		}

		var raw []struct {
			ID     string  `json:"id"`
			Title  string  `json:"title"`
			Number float64 `json:"number"`
			URL    string  `json:"url"`
			Pages  []struct {
				Number   int    `json:"number"`
				ImageURL string `json:"image_url"`
			} `json:"pages"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return false, err
		}
		if len(raw) == 0 {
			return false, nil
		}

		for _, ch := range raw {
			pages := make([]toon.Page, len(ch.Pages))
			for i, pg := range ch.Pages {
				pages[i] = toon.Page{Number: pg.Number, ImageURL: pg.ImageURL}
			}
			chapters = append(chapters, toon.Chapter{
				ID:     ch.ID,
				Title:  ch.Title,
				Number: ch.Number,
				URL:    ch.URL,
				Pages:  pages,
			})
		}
		return true, nil
	})

	if errors.Is(err, toon.ErrNotFound) {
		return nil, toon.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return chapters, nil
}
