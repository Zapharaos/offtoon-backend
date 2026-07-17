package asura

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
)

// NewDownloadParams builds the asura-specific DownloadParams for the given slug and chapter IDs.
func (c *Client) NewDownloadParams(slug string, chapterIDs []string) api.DownloadParams {
	return DownloadParams{Slug: slug, ChapterIDs: chapterIDs}
}

// Download returns the chapters (with pages) for the given source-specific params.
//
// Request: GET {baseURL}/api/series/{slug}/chapters/{chapterNumber}
func (c *Client) Download(ctx context.Context, params api.DownloadParams) ([]toon.Chapter, error) {
	dp, ok := params.(DownloadParams)
	if !ok {
		return nil, fmt.Errorf("%s: Download received wrong params type %T", Name, params)
	}

	var chapters []toon.Chapter
	err := c.TryURLs(ctx, func(baseURL string) (bool, error) {
		var results []toon.Chapter
		for _, chapterID := range dp.ChapterIDs {
			ch, err := c.downloadChapter(ctx, baseURL, dp.Slug, chapterID)
			if err != nil {
				return false, err
			}
			if ch == nil {
				// 404 on this URL → try next base URL
				return false, nil
			}
			results = append(results, *ch)
		}
		chapters = results
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return chapters, nil
}

func (c *Client) downloadChapter(ctx context.Context, baseURL, slug, chapterID string) (*toon.Chapter, error) {
	base := strings.TrimRight(baseURL, "/")

	ch, found, err := c.fetchChapterPages(ctx, base, slug, chapterID)
	if err != nil {
		return nil, err
	}
	if !found {
		// Try stripping a hash suffix from legacy slugs.
		stripped, hadSuffix := stripHashSuffix(slug)
		if !hadSuffix {
			return nil, nil
		}
		ch, found, err = c.fetchChapterPages(ctx, base, stripped, chapterID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, nil
		}
	}
	return ch, nil
}

func (c *Client) fetchChapterPages(ctx context.Context, base, slug, chapterID string) (*toon.Chapter, bool, error) {
	reqURL := fmt.Sprintf("%s/api/series/%s/chapters/%s", base, slug, chapterID)

	var resp apiChapterPages
	found, err := c.doJSON(ctx, reqURL, &resp)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}

	if resp.Data.AccessGate != "" {
		return nil, false, fmt.Errorf("%s: chapter %s of %q is locked (access gate: %q)",
			Name, chapterID, slug, resp.Data.AccessGate)
	}

	ch := resp.Data.Chapter
	numStr := strconv.FormatFloat(ch.Number, 'f', -1, 64)
	if numStr == "0" {
		numStr = chapterID
	}

	title := ch.Title
	if title == "" {
		title = "Chapter " + numStr
	}

	chapterURL := fmt.Sprintf("%s/comics/%s/%s", publicBaseURL, slug, ch.Slug)

	result := &toon.Chapter{
		ID:     numStr,
		Number: ch.Number,
		Title:  title,
		URL:    chapterURL,
	}

	for i, p := range ch.Pages {
		result.Pages = append(result.Pages, toon.Page{
			Number:   i + 1,
			ImageURL: p.URL,
		})
	}

	return result, true, nil
}
