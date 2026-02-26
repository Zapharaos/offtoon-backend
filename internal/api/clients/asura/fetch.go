package asura

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/internal/utils"
	"golang.org/x/net/html"
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
//
// Field extraction strategy (all semantic, no CSS class guessing):
//
//   - Title:         <span class="text-xl font-bold">
//   - Cover:         <img alt="poster">
//   - Description:   <span class="font-medium text-sm text-[#A2A2A2]"> → <p>
//     text nodes after the first <br/> are the synopsis;
//     text inside <strong><em> before the <br/> is the note.
//   - Meta blocks:   <div> with two <h3> children where the first h3 has
//     class "text-[#D9D9D9]" (label) and the second is the value.
//     Labels: Author, Artist, Serialization, Updated On.
//   - Status/Type:   same two-h3 pattern but label h3 has "text-[#A2A2A2]"
//     and no "font-medium" (distinguishes them from meta blocks).
//   - Genres:        <button> direct children of the div that immediately
//     follows a <h3> whose text is "Genres".
//   - Chapters:      custom <asura_fetch_response_chapters> elements.
func parseFetchPage(body []byte, slug, sourceURL string) (*toon.Toon, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("%s: parse HTML: %w", Name, err)
	}

	t := &toon.Toon{
		ID:        slug,
		Source:    toon.Source(Name),
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
				// Must have all three classes to avoid matching other spans.
				if hasClass(n, "font-medium") && hasClass(n, "text-sm") &&
					hasClass(n, "text-[#A2A2A2]") && t.Description == "" {
					t.Note, t.Description = parseDescriptionSpan(n)
				}
				// Rating: <span class="ml-1 text-xs">9.5</span>
				// The span is a direct sibling of star-icon <div>s inside the rating block.
				// We identify it as the only span whose sole text child is a float in [0,10]
				// and whose previous element siblings are all <div>s.
				if t.Rating == 0 {
					if r, ok := extractRatingSpan(n); ok {
						t.Rating = r
					}
				}

			case "img":
				// Cover: <img alt="poster">
				if getAttr(n, "alt") == "poster" && t.CoverURL == "" {
					t.CoverURL = getAttr(n, "src")
				}

			case "div":
				// Meta blocks: two direct h3 children, label h3 has text-[#D9D9D9] font-medium text-sm.
				if label, value := extractD9MetaBlock(n); label != "" {
					switch label {
					case "Author":
						if t.Author == "" {
							t.Author = value
						}
					case "Artist":
						if t.Artist == "" {
							t.Artist = value
						}
					case "Serialization":
						if t.Serialization == "" && value != "_" {
							t.Serialization = value
						}
					case "Updated On":
						if t.UpdatedOn == nil {
							t.UpdatedOn = utils.ParseAsuraDate(value)
						}
					}
				}

				// Status/Type blocks: two direct h3 children, label h3 has text-[#A2A2A2] text-sm
				// but NOT font-medium (that distinguishes them from the description span's context).
				if label, value := extractA2MetaBlock(n); label != "" {
					switch label {
					case "Status":
						if t.Status == "" {
							t.Status = toon.ParseStatus(value)
						}
					case "Type":
						if t.Type == "" {
							t.Type = value
						}
					}
				}

				// Genres: div following a <h3> whose text is "Genres",
				// containing <button> direct children.
				if t.Genres == nil {
					if genres := extractGenresFromDiv(n); len(genres) > 0 {
						t.Genres = genres
					}
				}

			case "asura_fetch_response_chapters":
				// legacy placeholder — no longer used
			}
		}

		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)

	t.Chapters = extractChapters(doc, sourceURL)

	if t.Title == "" {
		return nil, fmt.Errorf("%s: could not extract title from page", Name)
	}

	return t, nil
}

// extractD9MetaBlock extracts (label, value) from a <div> whose two direct
// <h3> children have the pattern:
//
//	<h3 class="text-[#D9D9D9] font-medium text-sm">Label</h3>
//	<h3 class="text-[#A2A2A2] text-sm">Value</h3>
func extractD9MetaBlock(n *html.Node) (label, value string) {
	h3s := directChildH3s(n)
	if len(h3s) < 2 {
		return "", ""
	}
	if !hasClass(h3s[0], "text-[#D9D9D9]") || !hasClass(h3s[0], "font-medium") || !hasClass(h3s[0], "text-sm") {
		return "", ""
	}
	return strings.TrimSpace(nodeText(h3s[0])), strings.TrimSpace(nodeText(h3s[1]))
}

// extractA2MetaBlock extracts (label, value) from a <div> whose two direct
// <h3> children have the pattern:
//
//	<h3 class="text-sm text-[#A2A2A2]">Label</h3>
//	<h3 class="text-sm ...">Value</h3>
//
// The label h3 must NOT have font-medium (to avoid matching description spans).
func extractA2MetaBlock(n *html.Node) (label, value string) {
	h3s := directChildH3s(n)
	if len(h3s) < 2 {
		return "", ""
	}
	lbl := h3s[0]
	if !hasClass(lbl, "text-[#A2A2A2]") || !hasClass(lbl, "text-sm") || hasClass(lbl, "font-medium") {
		return "", ""
	}
	return strings.TrimSpace(nodeText(lbl)), strings.TrimSpace(nodeText(h3s[1]))
}

// extractGenresFromDiv returns genre names when n is a div that is the sibling
// immediately following a <h3> whose text content is "Genres", and n contains
// <button> direct children.
func extractGenresFromDiv(n *html.Node) []string {
	// Check that the previous sibling (skipping text nodes) is <h3>Genres</h3>.
	prev := n.PrevSibling
	for prev != nil && prev.Type == html.TextNode {
		prev = prev.PrevSibling
	}
	if prev == nil || prev.Type != html.ElementNode || prev.Data != "h3" {
		return nil
	}
	if strings.TrimSpace(nodeText(prev)) != "Genres" {
		return nil
	}
	// Collect text from direct <button> children.
	var genres []string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "button" {
			if g := strings.TrimSpace(nodeText(c)); g != "" {
				genres = append(genres, g)
			}
		}
	}
	return genres
}

// parseDescriptionSpan splits the description span into two parts:
//
//   - note: the text inside <strong><em>...</em></strong> at the top of the
//     paragraph — this is typically a studio/publisher blurb.
//   - desc: all remaining text content of the paragraph (text nodes and inline
//     elements that are NOT the strong/em note), trimmed.
//
// Structure:
//
//	<span class="font-medium text-sm ...">
//	  <p>
//	    <strong><em>studio note</em></strong>
//	    <br/>
//	    synopsis text…
//	  </p>
//	</span>
func parseDescriptionSpan(span *html.Node) (note, desc string) {
	// Find the <p> child of the span.
	var p *html.Node
	for c := span.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "p" {
			p = c
			break
		}
	}
	if p == nil {
		// No <p> — fall back to full text.
		return "", strings.TrimSpace(nodeText(span))
	}

	var noteParts, descParts []string
	noteFound := false

	for c := p.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.ElementNode && c.Data == "strong" && !noteFound:
			// The first <strong> (which wraps <em>) is the studio note.
			noteParts = append(noteParts, strings.TrimSpace(nodeText(c)))
			noteFound = true

		case c.Type == html.ElementNode && c.Data == "br":
			// Skip <br> separators.

		case c.Type == html.TextNode:
			t := strings.TrimSpace(c.Data)
			if t != "" {
				descParts = append(descParts, t)
			}

		default:
			// Any other inline element after the note is part of the synopsis.
			t := strings.TrimSpace(nodeText(c))
			if t != "" {
				descParts = append(descParts, t)
			}
		}
	}

	return strings.Join(noteParts, " "), strings.Join(descParts, " ")
}

// extractRatingSpan returns the rating value if n is the rating <span>.
//
// The rating span has two stable properties:
//  1. Its sole text child is a decimal number in the range [0, 10].
//  2. Every previous element sibling is a <div> (the star icon wrappers).
//
// This combination is unique to the rating span and requires no CSS classes.
func extractRatingSpan(n *html.Node) (float64, bool) {
	// Must have exactly one child that is a non-empty text node.
	child := n.FirstChild
	if child == nil || child.NextSibling != nil || child.Type != html.TextNode {
		return 0, false
	}
	val, err := strconv.ParseFloat(strings.TrimSpace(child.Data), 64)
	if err != nil || val < 0 || val > 10 {
		return 0, false
	}
	// All previous element siblings must be <div> nodes (star icon wrappers).
	for sib := n.PrevSibling; sib != nil; sib = sib.PrevSibling {
		if sib.Type == html.TextNode {
			continue // skip whitespace text nodes
		}
		if sib.Type != html.ElementNode || sib.Data != "div" {
			return 0, false
		}
	}
	// Must have at least one <div> sibling (i.e. not just any lone span).
	hasDivSibling := false
	for sib := n.PrevSibling; sib != nil; sib = sib.PrevSibling {
		if sib.Type == html.ElementNode && sib.Data == "div" {
			hasDivSibling = true
			break
		}
	}
	if !hasDivSibling {
		return 0, false
	}
	return val, true
}

// directChildH3s returns the direct <h3> element children of n (not deeper descendants).
func directChildH3s(n *html.Node) []*html.Node {
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

// extractChapters locates the chapter list container and collects all chapters
// from it, excluding the "First Chapter" / "New Chapter" shortcut anchors.
//
// Structural anchor (semantic, no CSS classes):
//
//	The page has an <input> whose placeholder starts with "Search Chapter".
//	That input's next sibling <div> is the scrollable chapter list container.
//	Every direct child <div> of that container holds one chapter entry via a
//	nested <a href="{slug}/chapter/{number}"> anchor.
//
// Each chapter anchor contains:
//   - A <h3> whose text starts with "Chapter" — provides the chapter number.
//   - A second <h3> — provides the release date.
func extractChapters(doc *html.Node, sourceURL string) []toon.Chapter {
	container := findChapterListContainer(doc)
	if container == nil {
		return nil
	}

	origin := extractOrigin(sourceURL)
	var chapters []toon.Chapter

	// Each direct child <div> of the container is one chapter entry.
	for entry := container.FirstChild; entry != nil; entry = entry.NextSibling {
		if entry.Type != html.ElementNode || entry.Data != "div" {
			continue
		}
		// Find the <a href="...chapter/..."> inside the entry div.
		var anchor *html.Node
		for c := entry.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "a" && isChapterHref(getAttr(c, "href")) {
				anchor = c
				break
			}
		}
		if anchor == nil {
			continue
		}
		if ch := parseChapterAnchor(anchor, origin); ch != nil {
			chapters = append(chapters, *ch)
		}
	}

	return chapters
}

// findChapterListContainer returns the <div> that contains the full chapter
// list by finding the <input placeholder="Search Chapter..."> and returning
// its next sibling <div>.
func findChapterListContainer(doc *html.Node) *html.Node {
	var input *html.Node
	var findInput func(*html.Node)
	findInput = func(n *html.Node) {
		if input != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "input" {
			if strings.HasPrefix(getAttr(n, "placeholder"), "Search Chapter") {
				input = n
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findInput(c)
		}
	}
	findInput(doc)
	if input == nil {
		return nil
	}
	// The input is inside a wrapper div; the chapter list container is the
	// next sibling <div> of that wrapper div.
	wrapper := input.Parent
	if wrapper == nil {
		return nil
	}
	for sib := wrapper.NextSibling; sib != nil; sib = sib.NextSibling {
		if sib.Type == html.ElementNode && sib.Data == "div" {
			return sib
		}
	}
	return nil
}

// parseChapterAnchor extracts a toon.Chapter from a chapter list <a> node.
//
// The anchor contains:
//   - A decorative <div> (purple bar) — ignored.
//   - <h3> with text "Chapter N[title]" — chapter number and optional title.
//   - <h3> with the release date string.
func parseChapterAnchor(a *html.Node, origin string) *toon.Chapter {
	href := getAttr(a, "href")
	parts := strings.Split(strings.TrimRight(href, "/"), "/")
	if len(parts) < 2 {
		return nil
	}
	numStr := parts[len(parts)-1]
	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return nil
	}

	// Collect direct <h3> children for number and date.
	h3s := directChildH3s(a)
	var date *time.Time
	if len(h3s) >= 2 {
		date = utils.ParseAsuraDate(strings.TrimSpace(nodeText(h3s[1])))
	}

	return &toon.Chapter{
		ID:     numStr,
		Number: num,
		Title:  fmt.Sprintf("Chapter %s", numStr),
		Date:   date,
		URL:    origin + "/series/" + href,
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
