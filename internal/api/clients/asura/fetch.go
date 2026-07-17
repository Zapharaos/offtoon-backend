package asura

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"go.uber.org/zap"
)

// NewFetchParams builds the asura-specific FetchParams for the given slug.
func (c *Client) NewFetchParams(slug string) api.FetchParams {
	return FetchParams{Slug: slug}
}

// Fetch returns the full Toon details for the given source-specific params.
func (c *Client) Fetch(ctx context.Context, params api.FetchParams) (*toon.Toon, error) {
	fp, ok := params.(FetchParams)
	if !ok {
		return nil, fmt.Errorf("%s: Fetch received wrong params type %T", Name, params)
	}

	var result *toon.Toon
	err := c.TryURLs(ctx, func(baseURL string) (bool, error) {
		t, err := c.fetchFromURL(ctx, baseURL, fp.Slug)
		if err != nil {
			return false, err
		}
		if t == nil {
			return false, nil
		}
		result = t
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) fetchFromURL(ctx context.Context, baseURL, slug string) (*toon.Toon, error) {
	base := strings.TrimRight(baseURL, "/")

	// Try the slug as-is first; fall back to stripping a hash suffix for
	// slugs saved before the redesign (e.g. "some-toon-cd494674").
	slugToUse, found, err := c.fetchSeries(ctx, base, slug)
	if err != nil {
		return nil, err
	}
	if !found {
		// Retry once with the hash suffix stripped.
		stripped, hadSuffix := stripHashSuffix(slug)
		if !hadSuffix {
			return nil, nil
		}
		slugToUse, found, err = c.fetchSeries(ctx, base, stripped)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, nil
		}
	}

	// Fetch the chapter list.
	chaptersURL := fmt.Sprintf("%s/api/series/%s/chapters", base, slugToUse)
	var chapResp apiChaptersResponse
	chapFound, err := c.doJSON(ctx, chaptersURL, &chapResp)
	if err != nil {
		return nil, err
	}

	// Build the result using the last successful series fetch.
	seriesURL := fmt.Sprintf("%s/api/series/%s", base, slugToUse)
	var seriesResp apiSeriesResponse
	_, err = c.doJSONOnce(ctx, seriesURL, &seriesResp)
	if err != nil {
		return nil, err
	}

	t := mapToon(seriesResp.Series, slugToUse)

	if chapFound {
		skipped := 0
		for _, ch := range chapResp.Data {
			if ch.IsPremium || ch.IsLocked {
				skipped++
				continue
			}
			t.Chapters = append(t.Chapters, mapChapter(ch, slugToUse))
		}
		if skipped > 0 {
			zap.L().Debug("asura: skipped premium/locked chapters",
				zap.String("slug", slugToUse), zap.Int("count", skipped))
		}
	}

	return t, nil
}

// fetchSeries fetches the series JSON and returns the slug used (may differ
// from input after redirect), whether found, and any error.
func (c *Client) fetchSeries(ctx context.Context, base, slug string) (usedSlug string, found bool, err error) {
	seriesURL := fmt.Sprintf("%s/api/series/%s", base, slug)
	var resp apiSeriesResponse
	ok, err := c.doJSONOnce(ctx, seriesURL, &resp)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	// Prefer the slug returned by the API in case it was normalised server-side.
	if resp.Series.Slug != "" {
		return resp.Series.Slug, true, nil
	}
	return slug, true, nil
}

func mapToon(s apiSeries, slug string) *toon.Toon {
	t := &toon.Toon{
		ID:          slug,
		Title:       s.Title,
		Description: stripHTML(s.Description),
		CoverURL:    s.Cover,
		Status:      parseAPIStatus(s.Status),
		Type:        capitalise(s.Type),
		Rating:      s.Rating,
		Source:      toon.Source(Name),
		SourceURL:   publicBaseURL + s.PublicURL,
	}

	if s.Author != "_" {
		t.Author = s.Author
	}
	if s.Artist != "_" {
		t.Artist = s.Artist
	}

	// Prefer UpdatedAt; fall back to LastChapterAt.
	switch {
	case s.UpdatedAt != nil:
		t.UpdatedOn = s.UpdatedAt
	case s.LastChapterAt != nil:
		t.UpdatedOn = s.LastChapterAt
	}

	for _, g := range s.Genres {
		t.Genres = append(t.Genres, g.Name)
	}

	return t
}

func mapChapter(ch apiChapterMeta, seriesSlug string) toon.Chapter {
	numStr := strconv.FormatFloat(ch.Number, 'f', -1, 64)
	title := ch.Title
	if title == "" {
		title = "Chapter " + numStr
	}

	chapterURL := fmt.Sprintf("%s/comics/%s/%s", publicBaseURL, seriesSlug, ch.Slug)

	return toon.Chapter{
		ID:     numStr,
		Number: ch.Number,
		Title:  title,
		Date:   ch.PublishedAt,
		URL:    chapterURL,
	}
}

// capitalise upper-cases the first rune of s.
func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
