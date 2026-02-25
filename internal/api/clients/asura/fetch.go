package asura

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"golang.org/x/net/html"
)

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
		result = t
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// fetchFromURL performs the actual HTTP request and HTML parse for a single base URL.
func (c *Client) fetchFromURL(ctx context.Context, baseURL, slug string) (*toon.Toon, error) {
	url := strings.TrimRight(baseURL, "/") + "/series/" + slug

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", Name, err)
	}

	// Set headers matching the observed fetch request.
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	req.Header.Set("Cache-Control", "max-age=0")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := c.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("%s: HTTP request: %w", Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status %d for %s", Name, resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read body: %w", Name, err)
	}

	return parseFetchPage(body, slug, url)
}

// -----------------------------------------------------------------------
// HTML parsing
// -----------------------------------------------------------------------

// parseFetchPage parses the series detail page HTML into a toon.Toon.
func parseFetchPage(body []byte, slug, sourceURL string) (*toon.Toon, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("%s: parse HTML: %w", Name, err)
	}

	t := &toon.Toon{
		ID:        slug,
		Source:    Name,
		SourceURL: sourceURL,
	}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "span":
				// Title: <span class="text-xl font-bold">
				if hasClass(n, "text-xl") && hasClass(n, "font-bold") && t.Title == "" {
					t.Title = strings.TrimSpace(nodeText(n))
				}
				// Description: <span class="font-medium text-sm text-[#A2A2A2]">
				if hasClass(n, "font-medium") && hasClass(n, "text-sm") && t.Description == "" {
					t.Description = strings.TrimSpace(nodeText(n))
				}

			case "img":
				// Cover poster: <img alt="poster" ...>
				if getAttr(n, "alt") == "poster" && t.CoverURL == "" {
					t.CoverURL = getAttr(n, "src")
				}

			case "div":
				// Author/Artist metadata blocks:
				// <div><h3 class="text-[#D9D9D9] font-medium text-sm">Author</h3>
				//       <h3 class="text-[#A2A2A2] text-sm">I Stepped On Lego</h3></div>
				if isMetaBlock(n) {
					label, value := extractMetaBlock(n)
					switch label {
					case "Author":
						if t.Author == "" {
							t.Author = value
						}
					}
				}

			case "asura_fetch_response_chapters":
				// Chapter rows: <asura_fetch_response_chapters href="{slug}/chapter/{number}">
				href := getAttr(n, "href")
				if isChapterHref(href) {
					ch := parseChapterEntry(n, href, sourceURL)
					if ch != nil {
						t.Chapters = append(t.Chapters, *ch)
					}
				}
			}
		}

		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)

	if t.Title == "" {
		return nil, fmt.Errorf("%s: could not extract title from page", Name)
	}

	return t, nil
}

// isMetaBlock returns true when the div contains exactly two h3 children
// where the first has the label style and the second has the value style.
func isMetaBlock(n *html.Node) bool {
	h3s := childH3s(n)
	if len(h3s) < 2 {
		return false
	}
	return hasClass(h3s[0], "text-[#D9D9D9]") && hasClass(h3s[1], "text-[#A2A2A2]")
}

func extractMetaBlock(n *html.Node) (label, value string) {
	h3s := childH3s(n)
	if len(h3s) < 2 {
		return "", ""
	}
	return strings.TrimSpace(nodeText(h3s[0])), strings.TrimSpace(nodeText(h3s[1]))
}

func childH3s(n *html.Node) []*html.Node {
	var result []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "h3" {
			result = append(result, c)
		}
	}
	return result
}

// isChapterHref checks if the href matches the pattern "{slug}/chapter/{number}".
func isChapterHref(href string) bool {
	return strings.Contains(href, "/chapter/")
}

// parseChapterEntry extracts chapter data from asura_fetch_response_chapters chapter list <asura_fetch_response_chapters> node.
// href format: "revenge-of-the-iron-blooded-sword-hound-cd494674/chapter/151"
func parseChapterEntry(_ *html.Node, href, baseSourceURL string) *toon.Chapter {
	// Extract chapter number from the last path segment.
	parts := strings.Split(strings.TrimRight(href, "/"), "/")
	if len(parts) < 2 {
		return nil
	}
	numStr := parts[len(parts)-1]
	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return nil
	}

	// Build the canonical chapter URL from the source URL's host.
	// href is relative (no leading slash), so prefix with the base URL origin.
	origin := extractOrigin(baseSourceURL)
	chURL := origin + "/" + href

	return &toon.Chapter{
		ID:     numStr,
		Number: num,
		Title:  fmt.Sprintf("Chapter %s", numStr),
		URL:    chURL,
	}
}

// extractOrigin returns "https://host" from a full URL.
func extractOrigin(rawURL string) string {
	// Find the third slash which ends the origin.
	if idx := strings.Index(rawURL, "://"); idx != -1 {
		rest := rawURL[idx+3:]
		if slash := strings.Index(rest, "/"); slash != -1 {
			return rawURL[:idx+3+slash]
		}
		return rawURL // no path component
	}
	return rawURL
}

// -----------------------------------------------------------------------
// HTML utility helpers
// -----------------------------------------------------------------------

func hasClass(n *html.Node, cls string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == cls {
					return true
				}
			}
		}
	}
	return false
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// nodeText returns all text content of a node and its descendants.
func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// FetchWithExtraURLs behaves like Fetch but prepends extraURLs to the
// client's configured URL list for this call only.
func (c *Client) FetchWithExtraURLs(ctx context.Context, slug string, extraURLs []string) (*toon.Toon, error) {
	original := c.BaseClient
	c.BaseClient = c.BaseClient.WithExtraURLs(extraURLs)
	defer func() { c.BaseClient = original }()
	return c.Fetch(ctx, FetchParams{Slug: slug})
}
