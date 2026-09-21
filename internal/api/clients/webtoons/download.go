package webtoons

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"golang.org/x/net/html"
)

// NewDownloadParams builds the webtoons-specific DownloadParams for the given
// slug and episode numbers.
func (c *Client) NewDownloadParams(slug string, chapterIDs []string) api.DownloadParams {
	return DownloadParams{Slug: slug, ChapterIDs: chapterIDs}
}

// Download returns the episodes (with pages) for the given source-specific params.
//
// Request: GET {baseURL}/{lang}/{section}/{slug}/episode/viewer?title_no=...&episode_no=...
func (c *Client) Download(ctx context.Context, params api.DownloadParams) ([]toon.Chapter, error) {
	dp, ok := params.(DownloadParams)
	if !ok {
		return nil, fmt.Errorf("%s: Download received wrong params type %T", Name, params)
	}

	ref, err := parseToonID(dp.Slug)
	if err != nil {
		return nil, err
	}

	var chapters []toon.Chapter
	err = c.TryURLs(ctx, func(baseURL string) (bool, error) {
		results := make([]toon.Chapter, 0, len(dp.ChapterIDs))
		for _, episodeNo := range dp.ChapterIDs {
			ch, err := c.downloadEpisode(ctx, baseURL, ref, episodeNo)
			if err != nil {
				return false, err
			}
			if ch == nil {
				// 404 on this base URL → try the next one.
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

// downloadEpisode loads one viewer page and extracts its image list, retrying
// with the placeholder references when the stored genre or slug is stale.
// It returns nil when the episode is on none of them.
func (c *Client) downloadEpisode(ctx context.Context, baseURL string, ref toonRef, episodeNo string) (*toon.Chapter, error) {
	for _, candidate := range ref.altRefs() {
		doc, finalURL, found, err := c.doHTML(ctx, candidate.viewerURL(baseURL, c.language, episodeNo))
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		return buildChapter(doc, finalURL, episodeNo)
	}
	return nil, nil
}

// buildChapter extracts the page images from a parsed viewer page.
//
// The viewer lazy-loads its images: every <img class="_images"> carries a
// transparent placeholder in src and the real address in data-url.
func buildChapter(doc *html.Node, viewerURL, episodeNo string) (*toon.Chapter, error) {
	list := findFirst(doc, hasClass("_img_viewer_area"))
	if list == nil {
		list = doc
	}

	var pages []toon.Page
	for _, img := range findAll(list, all(hasTag("img"), hasClass("_images"))) {
		src := attr(img, "data-url")
		if src == "" {
			continue
		}
		pages = append(pages, toon.Page{
			Number:   len(pages) + 1,
			ImageURL: src,
		})
	}

	if len(pages) == 0 {
		// A free episode always renders its images server-side, so an empty
		// list means the episode is behind Fast Pass / Daily Pass or has been
		// taken down — either way, it is not downloadable.
		return nil, fmt.Errorf("%s: episode %s has no readable pages (it is likely paid or unavailable)", Name, episodeNo)
	}

	number, _ := strconv.ParseFloat(episodeNo, 64)

	title := textOfClass(doc, "subj_info_ell")
	if title == "" {
		title = metaContent(doc, "og:title")
	}
	if title == "" {
		title = "Episode " + episodeNo
	}

	return &toon.Chapter{
		ID:     episodeNo,
		Number: number,
		Title:  title,
		// URL is the canonical viewer address: the archiver sends it as the
		// Referer, which WEBTOON's image CDN requires (403 without one).
		URL:   viewerURL,
		Pages: pages,
	}, nil
}
