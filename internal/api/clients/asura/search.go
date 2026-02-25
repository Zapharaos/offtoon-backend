package asura

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"golang.org/x/net/html"
)

// Search returns toons whose title matches query across all configured URLs.
//
// Request: GET {baseURL}/series?page=1&name={query}
//
// The parser does not rely on any CSS or Tailwind classes.  Instead it uses
// two stable semantic anchors that are extremely unlikely to change:
//
//  1. <img alt="poster" src="..."> — every result card has exactly one of
//     these for its cover image.  Its parent <a href="/series/{slug}"> gives
//     us both the cover URL and the slug.
//
//  2. The first <a href="/series/{slug}"> with the same slug that contains
//     only text (no img child) is the title link.  It lives in the same card
//     container as the cover link.
//
// The slug (e.g. "revenge-of-the-iron-blooded-sword-hound-cd494674") is
// stored as SearchResult.ID so it can be fed directly into FetchParams.Slug.
func (c *Client) Search(ctx context.Context, query string) ([]toon.SearchResult, error) {
	var results []toon.SearchResult
	err := c.TryURLs(ctx, func(baseURL string) (bool, error) {
		res, err := c.searchFromURL(ctx, baseURL, query)
		if err != nil {
			return false, err
		}
		results = res
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// SearchWithExtraURLs behaves like Search but prepends extraURLs to the
// client's configured URL list for this call only.
func (c *Client) SearchWithExtraURLs(ctx context.Context, query string, extraURLs []string) ([]toon.SearchResult, error) {
	original := c.BaseClient
	c.BaseClient = c.BaseClient.WithExtraURLs(extraURLs)
	defer func() { c.BaseClient = original }()
	return c.Search(ctx, query)
}

// searchFromURL performs the HTTP request and HTML parse for a single base URL.
func (c *Client) searchFromURL(ctx context.Context, baseURL, query string) ([]toon.SearchResult, error) {
	searchURL := strings.TrimRight(baseURL, "/") + "/series?page=1&name=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build search request: %w", Name, err)
	}

	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	req.Header.Set("Cache-Control", "max-age=0")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := c.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("%s: search HTTP request: %w", Name, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status %d for search", Name, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read search body: %w", Name, err)
	}

	return parseSearchPage(body, baseURL)
}

// -----------------------------------------------------------------------
// HTML parsing — semantic-only, no CSS class dependencies
// -----------------------------------------------------------------------

// parseSearchPage walks the document looking for <img alt="poster"> nodes.
//
// Each such img lives inside an <a href="/series/{slug}"> (the cover link).
// That cover link's closest ancestor div that also contains a plain text
// <a href="/series/{slug}"> is the card container.  The text link's content
// is the title.
//
// Deduplication is applied so that the same slug is never returned twice
// (the page sometimes renders the same card in multiple tab panels).
func parseSearchPage(body []byte, baseURL string) ([]toon.SearchResult, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("%s: parse search HTML: %w", Name, err)
	}

	origin := extractOrigin(baseURL)

	var results []toon.SearchResult
	seen := make(map[string]struct{})

	// Collect all <img alt="poster"> nodes in document order.
	posterImgs := collectPosterImgs(doc)

	for _, img := range posterImgs {
		r := extractFromPosterImg(img, origin)
		if r == nil || r.ID == "" {
			continue
		}
		if _, dup := seen[r.ID]; dup {
			continue
		}
		seen[r.ID] = struct{}{}
		results = append(results, *r)
	}

	return results, nil
}

// collectPosterImgs returns every <img alt="poster"> node in the document.
func collectPosterImgs(doc *html.Node) []*html.Node {
	var imgs []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" && getAttr(n, "alt") == "poster" {
			imgs = append(imgs, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return imgs
}

// extractFromPosterImg derives a SearchResult from one <img alt="poster"> node.
//
// Traversal:
//  1. img.Parent  → <a href="/series/{slug}">  (cover link)
//  2. coverLink.Parent → <div> (cover wrapper)
//  3. coverWrapper.Parent → card <div> (contains both cover wrapper and info wrapper)
//  4. Within card <div>, find the first <a href="/series/{slug}"> that has
//     only text content (no img descendant) and the same slug → title link
func extractFromPosterImg(img *html.Node, origin string) *toon.SearchResult {
	// Step 1: parent must be <a href="/series/{slug}">
	coverLink := img.Parent
	if coverLink == nil || coverLink.Type != html.ElementNode || coverLink.Data != "a" {
		return nil
	}
	href := getAttr(coverLink, "href")
	slug := seriesSlugFromHref(href)
	if slug == "" {
		return nil
	}
	coverURL := getAttr(img, "src")

	// Step 2: cover wrapper div
	coverWrapper := coverLink.Parent
	if coverWrapper == nil {
		return nil
	}

	// Step 3: card container div (parent of cover wrapper)
	card := coverWrapper.Parent
	if card == nil {
		return nil
	}

	// Step 4: find the title link — an <a href="/series/{slug}"> with the same
	// slug that has non-empty text content and no <img> descendant.
	title := findTitleInCard(card, slug)

	return &toon.SearchResult{
		ID:        slug,
		Title:     title,
		CoverURL:  coverURL,
		Source:    Name,
		SourceURL: origin + "/series/" + slug,
	}
}

// findTitleInCard searches the card subtree for a text-only <a href="/series/{slug}">
// with the given slug.  "Text-only" means no <img> anywhere in its descendants.
func findTitleInCard(card *html.Node, slug string) string {
	var title string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if title != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			if seriesSlugFromHref(getAttr(n, "href")) == slug && !hasImgDescendant(n) {
				t := strings.TrimSpace(nodeText(n))
				if t != "" {
					title = t
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(card)
	return title
}

// seriesSlugFromHref extracts the slug from a /series/{slug} href.
// Returns "" for any other href (genre filters, etc.).
func seriesSlugFromHref(href string) string {
	if !strings.HasPrefix(href, "/series/") {
		return ""
	}
	slug := strings.TrimPrefix(href, "/series/")
	// Reject genre/filter hrefs like "/series?page=1&genres=1"
	// and any sub-paths like "/series/slug/chapter/1".
	if slug == "" || strings.ContainsAny(slug, "?/") {
		return ""
	}
	return slug
}

// hasImgDescendant returns true if any descendant of n is an <img> element.
func hasImgDescendant(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "img" {
			return true
		}
		if hasImgDescendant(c) {
			return true
		}
	}
	return false
}
