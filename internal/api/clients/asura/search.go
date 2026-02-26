package asura

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"golang.org/x/net/html"
)

// Search returns toons whose title matches query across all configured URLs.
//
// Request: GET {baseURL}/series?page=1&name={query}
//
// The parser does not rely on any CSS or Tailwind classes.  Instead it uses
// two stable semantic anchors:
//
//  1. <a href="series/{slug}"> — every result card is wrapped in one of these.
//     The slug is the path segment after "series/" with no leading slash,
//     no query string, and no sub-path (e.g. "the-dark-swordsman-returns-f9bb5f2a").
//
//  2. Inside the anchor, the first <img> element provides the cover URL via
//     its src attribute, and the first <span> whose only child is a text node
//     provides the title.
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

// searchFromURL performs HTTP requests and HTML parsing for all pages of
// results for a single base URL, accumulating results until no next page exists.
func (c *Client) searchFromURL(ctx context.Context, baseURL, query string) ([]toon.SearchResult, error) {
	var all []toon.SearchResult
	seen := make(map[string]struct{})

	for page := 1; ; page++ {
		pageURL := strings.TrimRight(baseURL, "/") +
			"/series?page=" + fmt.Sprintf("%d", page) +
			"&name=" + url.QueryEscape(query)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: build search request (page %d): %w", Name, page, err)
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
			return nil, fmt.Errorf("%s: search HTTP request (page %d): %w", Name, page, err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close() //nolint:errcheck
			return nil, fmt.Errorf("%s: unexpected status %d for search (page %d)", Name, resp.StatusCode, page)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close() //nolint:errcheck
		if err != nil {
			return nil, fmt.Errorf("%s: read search body (page %d): %w", Name, page, err)
		}

		results, hasNext, err := parseSearchPage(body, baseURL)
		if err != nil {
			return nil, err
		}

		for _, r := range results {
			if _, dup := seen[r.ID]; !dup {
				seen[r.ID] = struct{}{}
				all = append(all, r)
			}
		}

		if !hasNext {
			break
		}
	}

	return all, nil
}

// -----------------------------------------------------------------------
// HTML parsing — semantic-only, no CSS class dependencies
// -----------------------------------------------------------------------

// parseSearchPage locates the search results grid and extracts one
// SearchResult per card anchor. It also detects whether a next page exists.
//
// Structural anchor (semantic, not CSS-dependent):
//
//	The page contains a <form id="hook-form">.  That form lives inside a
//	filter/search section.  The results grid <div> is the next sibling <div>
//	of the filter section's direct parent.  Only anchors inside that grid are
//	considered — this prevents toons from sidebar sections (e.g. "Popular")
//	from leaking into the results.
//
// Next-page detection:
//
//	The pagination "Next" anchor has style="pointer-events:auto" when a next
//	page exists, and style="pointer-events:none" when it does not.  No CSS
//	classes are used for this check.
//
// Each card anchor is <a href="series/{slug}"> and contains:
//   - an <img> whose src is the cover URL
//   - a <span> whose sole child is a text node — that text is the title
func parseSearchPage(body []byte, baseURL string) ([]toon.SearchResult, bool, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, false, fmt.Errorf("%s: parse search HTML: %w", Name, err)
	}

	origin := extractOrigin(baseURL)

	// Locate the results grid using <form id="hook-form"> as the anchor.
	resultsGrid := findResultsGrid(doc)
	if resultsGrid == nil {
		// Fallback: no form found — scan the whole document (future-proofing).
		resultsGrid = doc
	}

	var results []toon.SearchResult

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			slug := seriesSlugFromHref(getAttr(n, "href"))
			if slug != "" {
				coverURL, status := findImgSrcAndStatus(n)
				title, lastChapter, rating := findInfoContainerFields(n)
				results = append(results, toon.SearchResult{
					ID:          slug,
					Title:       title,
					CoverURL:    coverURL,
					Status:      status,
					LastChapter: lastChapter,
					Rating:      rating,
					Source:      toon.Source(Name),
					SourceURL:   origin + "/series/" + slug,
				})
				// Do not recurse into a card anchor — its children are not cards.
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(resultsGrid)

	hasNext := hasNextPage(doc)

	return results, hasNext, nil
}

// hasNextPage reports whether the page contains an active "Next" pagination
// anchor. The anchor is considered active when its style attribute contains
// "pointer-events:auto" (disabled pages use "pointer-events:none").
// This is a semantic check — no CSS class names are involved.
func hasNextPage(doc *html.Node) bool {
	var found bool
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			style := getAttr(n, "style")
			if strings.Contains(style, "pointer-events:auto") && strings.Contains(nodeText(n), "Next") {
				found = true
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return found
}

// findResultsGrid locates the <div> that contains only the search result cards.
//
// Strategy:
//  1. Find <form id="hook-form"> — a stable semantic element unique to the
//     filter/search bar section.
//  2. Walk up to the grandparent of that form — this is the filter section div
//     that is a direct child of the shared container.
//  3. The results grid is the next sibling <div> of that filter section.
func findResultsGrid(doc *html.Node) *html.Node {
	form := findFormByID(doc, "hook-form")
	if form == nil {
		return nil
	}

	// Walk up: form → wrapper div → wrapper div → filter section div.
	// The exact depth may vary, so we walk up until we find a sibling <div>
	// that directly contains <a href="series/..."> children.
	node := form
	for node.Parent != nil {
		node = node.Parent
		// Check each subsequent sibling of this node for a div that contains
		// card anchors as direct children.
		for sib := node.NextSibling; sib != nil; sib = sib.NextSibling {
			if sib.Type != html.ElementNode || sib.Data != "div" {
				continue
			}
			if divContainsCardAnchors(sib) {
				return sib
			}
		}
	}
	return nil
}

// findFormByID returns the first <form> element with the given id attribute.
func findFormByID(doc *html.Node, id string) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "form" && getAttr(n, "id") == id {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return found
}

// divContainsCardAnchors returns true if the given div has at least one
// direct child <a> element whose href matches the series/{slug} pattern.
func divContainsCardAnchors(div *html.Node) bool {
	for c := div.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "a" {
			if seriesSlugFromHref(getAttr(c, "href")) != "" {
				return true
			}
		}
	}
	return false
}

// findImgSrcAndStatus returns the cover image URL and the publication status
// from a card anchor node.
//
// Both pieces of data live in the IMAGE CONTAINER — the first <div> inside the
// card anchor that contains the <img>:
//
//	<div class="flex h-[250px]...">          ← IMAGE CONTAINER
//	  <span class="status bg-blue-700">Ongoing</span>
//	  <img src="..." />
//	  <div>...</div>                          ← platform badge
//	</div>
//
// The status badge is a <span> whose class attribute starts with "status ".
// Its sole text child is the raw status string (e.g. "Ongoing", "Season End").
func findImgSrcAndStatus(n *html.Node) (coverURL string, status toon.Status) {
	img := findFirstImg(n)
	if img == nil {
		return "", toon.StatusUnknown
	}

	coverURL = getAttr(img, "src")

	// Walk up to the nearest <div> ancestor of the img (the image container).
	imgContainer := img.Parent
	for imgContainer != nil && !(imgContainer.Type == html.ElementNode && imgContainer.Data == "div") {
		imgContainer = imgContainer.Parent
	}
	if imgContainer == nil {
		return coverURL, toon.StatusUnknown
	}

	// Look for a <span class="status ..."> direct child of the image container.
	for c := imgContainer.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "span" {
			continue
		}
		cls := getAttr(c, "class")
		if strings.HasPrefix(cls, "status") {
			raw := strings.TrimSpace(nodeText(c))
			return coverURL, toon.ParseStatus(raw)
		}
	}

	return coverURL, toon.StatusUnknown
}

// findInfoContainerFields returns the title, chapter count, and rating from a
// card anchor node. All three come from the INFO CONTAINER — the <div> sibling
// that follows the image container:
//
//	<div class="block w-[100%]...">          ← INFO CONTAINER
//	  <span class="block ...">The Title</span>
//	  <span class="text-[13px]...">Chapter <!-- -->24</span>
//	  <span class="flex ...">               ← rating row
//	    ... (star icons) ...
//	    <span class="ml-1 text-xs">9.3</span>
//	  </span>
//	</div>
//
// The chapter span contains the text "Chapter", a React comment node (<!-- -->),
// and then the chapter number as a text node. Only the numeric part is returned.
// The rating is the text of the last <span class="ml-1 text-xs"> in the container.
func findInfoContainerFields(n *html.Node) (title string, lastChapter float64, rating float64) {
	img := findFirstImg(n)
	if img == nil {
		return "", 0, 0
	}

	// Walk up to the nearest <div> ancestor of the img (image container).
	imgContainer := img.Parent
	for imgContainer != nil && !(imgContainer.Type == html.ElementNode && imgContainer.Data == "div") {
		imgContainer = imgContainer.Parent
	}
	if imgContainer == nil {
		return "", 0, 0
	}

	// The info container is the next sibling <div> after the image container.
	var infoContainer *html.Node
	for sib := imgContainer.NextSibling; sib != nil; sib = sib.NextSibling {
		if sib.Type == html.ElementNode && sib.Data == "div" {
			infoContainer = sib
			break
		}
	}
	if infoContainer == nil {
		return "", 0, 0
	}

	// Iterate direct <span> children of the info container in order:
	//   1st span → title (sole text-node child)
	//   2nd span → "Chapter <!-- -->N"
	//   3rd span → rating row (contains <span class="ml-1 text-xs">N.N</span>)
	spanIdx := 0
	for c := infoContainer.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "span" {
			continue
		}
		spanIdx++
		switch spanIdx {
		case 1:
			// Title: sole text-node child.
			if ch := c.FirstChild; ch != nil && ch.NextSibling == nil && ch.Type == html.TextNode {
				title = strings.TrimSpace(ch.Data)
			}
		case 2:
			// Chapter: "Chapter <!-- -->74.5" — scan all text nodes, pick the
			// last non-empty one that is purely numeric (int or float).
			var lastNumericText string
			var scanChapter func(*html.Node)
			scanChapter = func(nd *html.Node) {
				if nd.Type == html.TextNode {
					t := strings.TrimSpace(nd.Data)
					if t != "" && t != "Chapter" {
						lastNumericText = t
					}
				}
				for ch := nd.FirstChild; ch != nil; ch = ch.NextSibling {
					scanChapter(ch)
				}
			}
			scanChapter(c)
			if lastNumericText != "" {
				if v, err := strconv.ParseFloat(lastNumericText, 64); err == nil {
					lastChapter = v
				}
			}
		case 3:
			// Rating row: find <span class="ml-1 text-xs">N.N</span>.
			var findRating func(*html.Node)
			findRating = func(nd *html.Node) {
				if nd.Type == html.ElementNode && nd.Data == "span" {
					if getAttr(nd, "class") == "ml-1 text-xs" {
						if ch := nd.FirstChild; ch != nil && ch.Type == html.TextNode {
							if v, err := strconv.ParseFloat(strings.TrimSpace(ch.Data), 64); err == nil {
								rating = v
							}
						}
					}
				}
				for ch := nd.FirstChild; ch != nil; ch = ch.NextSibling {
					findRating(ch)
				}
			}
			findRating(c)
		}
	}

	return title, lastChapter, rating
}

// findFirstImg returns the first <img> element found anywhere inside n.
func findFirstImg(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.Data == "img" {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if img := findFirstImg(c); img != nil {
			return img
		}
	}
	return nil
}

// seriesSlugFromHref extracts the slug from a series/{slug} href.
// Supports both relative ("series/{slug}") and absolute ("/series/{slug}") forms.
// Returns "" for any other href (filter hrefs, sub-paths, etc.).
func seriesSlugFromHref(href string) string {
	// Strip optional leading slash so both forms are handled uniformly.
	href = strings.TrimPrefix(href, "/")
	if !strings.HasPrefix(href, "series/") {
		return ""
	}
	slug := strings.TrimPrefix(href, "series/")
	// Reject filter hrefs like "series?page=1&genres=1"
	// and sub-paths like "series/slug/chapter/1".
	if slug == "" || strings.ContainsAny(slug, "?/") {
		return ""
	}
	return slug
}
