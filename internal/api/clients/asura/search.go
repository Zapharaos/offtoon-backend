package asura

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"go.uber.org/zap"
)

// Search returns toons whose title matches query across all configured URLs.
//
// Request: GET {baseURL}/api/search?q={query}&limit=20&offset=N
func (c *Client) Search(ctx context.Context, query string) ([]toon.SearchResult, error) {
	var results []toon.SearchResult
	err := c.TryURLs(ctx, func(baseURL string) (bool, error) {
		res, err := c.searchFromURL(ctx, baseURL, query)
		if err != nil {
			return false, err
		}
		if res == nil {
			return false, nil
		}
		results = res
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

const searchPageSize = 20
const searchMaxResults = 200

func (c *Client) searchFromURL(ctx context.Context, baseURL, query string) ([]toon.SearchResult, error) {
	base := strings.TrimRight(baseURL, "/")
	var all []toon.SearchResult
	seen := make(map[string]struct{})

	for offset := 0; ; offset += searchPageSize {
		if offset >= searchMaxResults {
			zap.L().Warn("asura: search hit max results cap, stopping pagination",
				zap.String("query", query), zap.Int("cap", searchMaxResults))
			break
		}

		reqURL := fmt.Sprintf("%s/api/search?q=%s&limit=%d&offset=%d",
			base, url.QueryEscape(query), searchPageSize, offset)

		var resp apiSearchResponse
		found, err := c.doJSON(ctx, reqURL, &resp)
		if err != nil {
			return nil, err
		}
		if !found {
			// 404 on first page → no results on this URL
			if offset == 0 {
				return nil, nil
			}
			break
		}

		if len(resp.Data) == 0 {
			if offset == 0 {
				return nil, nil
			}
			break
		}

		for _, s := range resp.Data {
			if _, dup := seen[s.Slug]; dup {
				continue
			}
			seen[s.Slug] = struct{}{}
			all = append(all, mapSearchResult(s))
		}

		if !resp.Meta.HasMore {
			break
		}
	}

	return all, nil
}

func mapSearchResult(s apiSeries) toon.SearchResult {
	return toon.SearchResult{
		ID:          s.Slug,
		Title:       s.Title,
		CoverURL:    s.Cover,
		Status:      parseAPIStatus(s.Status),
		LastChapter: float64(s.ChapterCount),
		Rating:      s.Rating,
		Source:      toon.Source(Name),
		SourceURL:   publicBaseURL + s.PublicURL,
	}
}
