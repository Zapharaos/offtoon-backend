package webtoons

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"go.uber.org/zap"
	"golang.org/x/net/html"
)

// maxListPages caps how many episode-list pages are walked for one series, so a
// malformed paginator can never turn into an unbounded crawl.  WEBTOON renders
// about ten episodes per page, which leaves room for several thousand of them.
const maxListPages = 500

// NewFetchParams builds the webtoons-specific FetchParams for the given slug.
func (c *Client) NewFetchParams(slug string) api.FetchParams {
	return FetchParams{Slug: slug}
}

// Fetch returns the full Toon details for the given source-specific params.
func (c *Client) Fetch(ctx context.Context, params api.FetchParams) (*toon.Toon, error) {
	fp, ok := params.(FetchParams)
	if !ok {
		return nil, fmt.Errorf("%s: Fetch received wrong params type %T", Name, params)
	}

	ref, err := parseToonID(fp.Slug)
	if err != nil {
		return nil, err
	}

	var result *toon.Toon
	err = c.TryURLs(ctx, func(baseURL string) (bool, error) {
		t, err := c.fetchFromURL(ctx, baseURL, ref)
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

// fetchFromURL loads the series page (retrying with the placeholder references
// when the stored genre or slug has gone stale) and then walks the episode
// list.  It returns nil when the series is not on this base URL.
func (c *Client) fetchFromURL(ctx context.Context, baseURL string, ref toonRef) (*toon.Toon, error) {
	var (
		doc      *html.Node
		finalURL string
	)

	for _, candidate := range ref.altRefs() {
		d, u, found, err := c.doHTML(ctx, candidate.listURL(baseURL, c.language, 1))
		if err != nil {
			return nil, err
		}
		if found {
			doc, finalURL = d, u
			break
		}
	}
	if doc == nil {
		return nil, nil
	}

	// The response may have been redirected to the canonical URL; adopt the
	// section and slug it resolved to so the stored ID self-heals.
	canonical := ref
	if r, ok := refFromURL(finalURL); ok {
		canonical = r
	}

	t := mapToon(doc, canonical, finalURL)

	chapters, err := c.fetchEpisodes(ctx, baseURL, canonical, doc)
	if err != nil {
		return nil, err
	}
	t.Chapters = chapters

	return t, nil
}

// fetchEpisodes returns every episode of a series, in ascending order.
//
// It prefers the JSON endpoint, which answers in a single request, and falls
// back to walking the paginated HTML list for the series it does not serve
// (Canvas) or when it is unavailable.  firstPage is the already-parsed page 1
// of that HTML list, so the fallback costs nothing extra.
func (c *Client) fetchEpisodes(ctx context.Context, baseURL string, ref toonRef, firstPage *html.Node) ([]toon.Chapter, error) {
	if c.canUseEpisodeAPI(ref) {
		chapters, found, err := c.fetchEpisodesAPI(ctx, ref)
		switch {
		case err != nil:
			// The HTML list is slower but always there; a hiccup on the JSON
			// endpoint should degrade the fetch, not fail it.
			zap.L().Warn("webtoons: episode API failed, falling back to the HTML list",
				zap.String("toon", ref.String()), zap.Error(err))
		case found:
			return chapters, nil
		}
	}
	return c.fetchEpisodesHTML(ctx, baseURL, ref, firstPage)
}

// fetchEpisodesHTML walks every page of the paginated episode list.
func (c *Client) fetchEpisodesHTML(ctx context.Context, baseURL string, ref toonRef, firstPage *html.Node) ([]toon.Chapter, error) {
	byID := make(map[string]toon.Chapter)

	collect := func(doc *html.Node) {
		for _, item := range findAll(doc, hasClass("_episodeItem")) {
			ch, ok := mapEpisode(item)
			if !ok {
				continue
			}
			if _, dup := byID[ch.ID]; !dup {
				byID[ch.ID] = ch
			}
		}
	}

	collect(firstPage)

	// The list is walked one page at a time until a page stops contributing.
	// Neither shortcut works here: the paginator only renders a sliding window
	// of ten links and its "next" arrow jumps a whole block (1 → 11 → 21), and
	// there is no last-page marker to read — WEBTOON clamps an out-of-range
	// page to the last one and re-renders it, which is what ends the loop.
	page := 1
	for page < maxListPages {
		page++

		doc, _, found, err := c.doHTML(ctx, ref.listURL(baseURL, c.language, page))
		if err != nil {
			return nil, err
		}
		if !found {
			break
		}

		before := len(byID)
		collect(doc)
		if len(byID) == before {
			break // past the end: this page only repeated episodes we have
		}
	}

	if page >= maxListPages {
		zap.L().Warn("webtoons: episode list hit the page cap, some episodes may be missing",
			zap.String("toon", ref.String()), zap.Int("cap", maxListPages))
	}

	chapters := make([]toon.Chapter, 0, len(byID))
	for _, ch := range byID {
		chapters = append(chapters, ch)
	}
	sortChapters(chapters)
	return chapters, nil
}

// sortChapters orders episodes by number, oldest first.
func sortChapters(chapters []toon.Chapter) {
	sort.Slice(chapters, func(i, j int) bool {
		if chapters[i].Number != chapters[j].Number {
			return chapters[i].Number < chapters[j].Number
		}
		return chapters[i].ID < chapters[j].ID
	})
}

// -----------------------------------------------------------------------
// Mapping
// -----------------------------------------------------------------------

// mapToon builds the Toon metadata from a parsed series page.
//
// The Open Graph tags carry the title, synopsis and cover in a stable, already
// unescaped form, so they are preferred over the visible markup; the genre,
// author and publication status have no meta equivalent and are read from the
// page body.
func mapToon(doc *html.Node, ref toonRef, sourceURL string) *toon.Toon {
	title := metaContent(doc, "og:title")
	if title == "" {
		title = textOfClass(doc, "subj")
	}

	description := metaContent(doc, "og:description")
	if description == "" {
		description = textOfClass(doc, "summary")
	}

	cover := metaContent(doc, "og:image")
	if header := findFirst(doc, hasClass("detail_header")); cover == "" && header != nil {
		cover = imgSrc(header)
	}
	cover = browserSafeImageURL(cover)

	author := metaContent(doc, "com-linewebtoon:webtoon:author")
	if author == "" {
		author = textOfClass(doc, "author_area")
		// The author block ends with the "author info" button label.
		author = strings.TrimSpace(strings.TrimSuffix(author, "author info"))
	}

	kind := "Originals"
	if ref.isCanvas() {
		kind = "Canvas"
	}

	t := &toon.Toon{
		ID:          ref.String(),
		Title:       title,
		Author:      author,
		Description: description,
		CoverURL:    cover,
		Status:      parseDayInfo(findFirst(doc, hasClass("day_info"))),
		Type:        kind,
		Source:      toon.Source(Name),
		SourceURL:   sourceURL,
	}

	if genre := textOfClass(doc, "genre"); genre != "" {
		t.Genres = append(t.Genres, genre)
	}

	return t
}

// mapEpisode turns one <li class="_episodeItem"> into a toon.Chapter.
// It reports false when the item carries no episode number.
func mapEpisode(item *html.Node) (toon.Chapter, bool) {
	episodeNo := attr(item, "data-episode-no")
	if !isDigits(episodeNo) {
		return toon.Chapter{}, false
	}

	title := textOfClass(item, "subj")
	if title == "" {
		title = "Episode " + episodeNo
	}

	number, _ := strconv.ParseFloat(episodeNo, 64)

	ch := toon.Chapter{
		ID:     episodeNo,
		Title:  title,
		Number: number,
		URL:    absURL(attr(findFirst(item, hasClass("detail_list_link")), "href")),
	}

	if d := parseEpisodeDate(textOfClass(item, "date")); d != nil {
		ch.Date = d
	}

	return ch, true
}

// episodeDateLayouts are the publication-date formats WEBTOON renders, which
// vary with the content language.  An unparsed date is simply omitted.
var episodeDateLayouts = []string{
	"Jan 2, 2006",
	"2 Jan 2006",
	"Jan 2. 2006",
	"2006.01.02",
	"2006-01-02",
	"02/01/2006",
}

// parseEpisodeDate parses a rendered publication date, returning nil when the
// format is not one we recognise.
func parseEpisodeDate(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	for _, layout := range episodeDateLayouts {
		if d, err := time.Parse(layout, raw); err == nil {
			return &d
		}
	}
	return nil
}
