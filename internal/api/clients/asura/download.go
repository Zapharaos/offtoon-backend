package asura

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"golang.org/x/net/html"
)

// -----------------------------------------------------------------------
// Download
// -----------------------------------------------------------------------

// NewDownloadParams builds the asura-specific DownloadParams for the given slug and chapter IDs.
func (c *Client) NewDownloadParams(slug string, chapterIDs []string) api.DownloadParams {
	return DownloadParams{Slug: slug, ChapterIDs: chapterIDs}
}

// Download returns the chapters (with pages) for the given source-specific params.
//
// The chapter URL is constructed deterministically as:
//
//	{baseURL}/series/{slug}/chapter/{chapterID}
//
// where chapterID is the chapter number returned by Fetch (e.g. "151").
// No re-fetch of the series page is needed — slug + chapterID is sufficient.
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

// downloadChapter fetches a single chapter page and extracts its image URLs
// from the Next.js RSC payload embedded in the HTML.
//
// URL: {baseURL}/series/{slug}/chapter/{chapterID}
func (c *Client) downloadChapter(ctx context.Context, baseURL, slug, chapterID string) (*toon.Chapter, error) {
	chURL := strings.TrimRight(baseURL, "/") + "/series/" + slug + "/chapter/" + chapterID

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, chURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build chapter request: %w", Name, err)
	}

	// Mirror the headers from the observed chapter request.
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	req.Header.Set("Cache-Control", "max-age=0")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Referer", strings.TrimRight(baseURL, "/")+"/series/"+slug)

	resp, err := c.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("%s: chapter HTTP request: %w", Name, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status %d for chapter %s", Name, resp.StatusCode, chapterID)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read chapter body: %w", Name, err)
	}

	num, _ := strconv.ParseFloat(chapterID, 64)
	return parseChapterPage(body, chapterID, num, chURL)
}

// -----------------------------------------------------------------------
// RSC payload parsing
// -----------------------------------------------------------------------

// rscChapterData mirrors the shape of the "chapter" object embedded in the
// Next.js RSC __next_f payload on the chapter reader page.
//
// JSON shape (from asura_chapter_response.json):
//
//	"chapter": {
//	  "id":      28055,
//	  "name":    8,       // chapter number
//	  "title":   null,
//	  "pages": [
//	    { "order": 1, "url": "https://gg.asuracomic.net/storage/..." },
//	    ...
//	  ]
//	}
type rscChapterData struct {
	Chapter struct {
		ID    int     `json:"id"`
		Name  float64 `json:"name"`
		Title *string `json:"title"`
		Pages []struct {
			Order int    `json:"order"`
			URL   string `json:"url"`
		} `json:"pages"`
	} `json:"chapter"`
}

// parseChapterPage parses the chapter reader page HTML, extracts the JSON
// blob embedded inside the Next.js RSC <script> tag, and maps the page URLs
// to a toon.Chapter.
//
// The page embeds data via:
//
//	<script>self.__next_f.push([1, "19:[\"$\",\"div\",...{\"chapter\":{...\"pages\":[...]}}...]"])</script>
//
// The inner string is a JSON-encoded RSC tree that contains the chapter
// object with its pages list.
func parseChapterPage(body []byte, chapterID string, num float64, chURL string) (*toon.Chapter, error) {
	// 1. Parse the HTML to find all <script> text nodes.
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("%s: parse chapter HTML: %w", Name, err)
	}

	scripts := collectScriptContents(doc)

	// 2. Among those scripts, find the one that contains the chapter's "pages"
	//    key inside a __next_f.push call, then extract the inner JSON string.
	var chapterData *rscChapterData
	for _, script := range scripts {
		// The pages key may appear as "pages" (plain) or \"pages\" (escaped
		// inside a JSON string argument to __next_f.push).
		hasPages := strings.Contains(script, `"pages"`) || strings.Contains(script, `\"pages\"`)
		if !hasPages || !strings.Contains(script, `__next_f`) {
			continue
		}

		data, err := extractRSCChapterData(script)
		if err != nil || data == nil {
			continue
		}
		chapterData = data
		break
	}

	if chapterData == nil {
		return nil, fmt.Errorf("%s: could not find chapter page data in RSC payload for chapter %s", Name, chapterID)
	}

	// 3. Build the toon.Chapter from the parsed data.
	ch := &toon.Chapter{
		ID:     chapterID,
		Number: num,
		URL:    chURL,
	}

	if chapterData.Chapter.Title != nil && *chapterData.Chapter.Title != "" {
		ch.Title = *chapterData.Chapter.Title
	} else {
		ch.Title = fmt.Sprintf("Chapter %s", chapterID)
	}

	for _, p := range chapterData.Chapter.Pages {
		ch.Pages = append(ch.Pages, toon.Page{
			Number:   p.Order,
			ImageURL: p.URL,
		})
	}

	return ch, nil
}

// collectScriptContents walks the HTML tree and returns the text content of
// every <script> element.
func collectScriptContents(doc *html.Node) []string {
	var scripts []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" {
			scripts = append(scripts, nodeText(n))
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return scripts
}

// extractRSCChapterData finds the JSON object containing "chapter"→"pages"
// inside a raw __next_f.push script string and unmarshals it.
//
// The script looks like:
//
//	self.__next_f.push([1, "19:[...,{\"chapter\":{...\"pages\":[...]}},...\""])
//
// The second element of the outer array is a JSON-encoded string whose
// content is the RSC tree. We locate the {"chapter":{"pages":[...]}} object
// by scanning for the "pages" key and then finding the enclosing object
// boundaries.
//
// Two formats are handled:
//  1. The RSC payload is a JSON-encoded string inside the push call: quotes
//     are escaped as \" so the pages array anchor is \"pages\":[.
//  2. The RSC payload is already plain JSON (unescaped).
func extractRSCChapterData(script string) (*rscChapterData, error) {
	// If the pages array is escaped (\"pages\":[) the whole RSC payload is a
	// JSON string value. Unescape it and retry.
	if strings.Contains(script, `\"pages\":[`) && !strings.Contains(script, `"pages":[`) {
		unescaped, err := unescapeRSCString(script)
		if err == nil && strings.Contains(unescaped, `"pages":[`) {
			return extractRSCChapterData(unescaped)
		}
	}

	// Find the position of `"pages":[` to anchor our search.
	pagesKey := `"pages":[`
	pagesIdx := strings.Index(script, pagesKey)
	if pagesIdx < 0 {
		return nil, nil
	}

	// Walk left from pagesIdx to find the opening `{` of the object that
	// contains both "chapter" and "pages". We look for `"chapter":{` which
	// always precedes "pages" in the payload.
	chapterKey := `"chapter":{"id":`
	chapterIdx := strings.LastIndex(script[:pagesIdx], chapterKey)
	if chapterIdx < 0 {
		chapterKey = `"chapter":{`
		chapterIdx = strings.LastIndex(script[:pagesIdx], chapterKey)
	}
	if chapterIdx < 0 {
		return nil, nil
	}

	// Walk backward from chapterIdx to find the enclosing `{` at depth 0.
	// This is more robust than string-searching for a specific key like "comic"
	// which may not be present in all chapter payloads.
	outerStart := findEnclosingObject(script, chapterIdx)
	if outerStart < 0 {
		// Fallback: use the chapter object itself.
		outerStart = chapterIdx
	}

	// Now find the matching closing `}` by counting brace depth.
	jsonFrag := script[outerStart:]
	end := findMatchingBrace(jsonFrag)
	if end < 0 {
		return nil, fmt.Errorf("unmatched brace in RSC payload")
	}

	raw := jsonFrag[:end+1]

	var data rscChapterData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		// The outer object may contain keys we don't model — try extracting
		// just the "chapter" sub-object and wrapping it.
		chStart := strings.Index(raw, `"chapter":{`)
		if chStart < 0 {
			return nil, fmt.Errorf("unmarshal RSC chapter data: %w", err)
		}
		chFrag := raw[chStart+len(`"chapter":`):]
		chEnd := findMatchingBrace(chFrag)
		if chEnd < 0 {
			return nil, fmt.Errorf("unmarshal RSC chapter data: %w", err)
		}
		wrapped := `{"chapter":` + chFrag[:chEnd+1] + `}`
		if err2 := json.Unmarshal([]byte(wrapped), &data); err2 != nil {
			return nil, fmt.Errorf("unmarshal RSC chapter data: %w", err2)
		}
	}

	if len(data.Chapter.Pages) == 0 {
		return nil, nil
	}

	return &data, nil
}

// unescapeRSCString extracts and unescapes the JSON string payload inside a
// __next_f.push([1, "...escaped RSC..."])  script tag.
//
// The RSC tree is transmitted as a JSON-encoded string argument, so all inner
// double quotes are escaped as \". We locate the opening `"` after `[1,` (or
// `[1, `) and the corresponding closing `"`, then JSON-decode that string so
// the caller receives plain, unescaped JSON/RSC text.
func unescapeRSCString(script string) (string, error) {
	// Find the start of the JSON string value: look for `[1,` followed by the
	// first `"` character (allowing optional whitespace).
	marker := `[1,`
	markerIdx := strings.Index(script, marker)
	if markerIdx < 0 {
		return "", fmt.Errorf("no [1, marker found")
	}
	start := markerIdx + len(marker)
	// Skip whitespace.
	for start < len(script) && (script[start] == ' ' || script[start] == '\t' || script[start] == '\n' || script[start] == '\r') {
		start++
	}
	if start >= len(script) || script[start] != '"' {
		return "", fmt.Errorf("no opening quote after [1,")
	}

	// Find the matching closing quote, respecting escape sequences.
	end := start + 1
	for end < len(script) {
		ch := script[end]
		if ch == '\\' {
			end += 2 // skip escaped character
			continue
		}
		if ch == '"' {
			break
		}
		end++
	}
	if end >= len(script) {
		return "", fmt.Errorf("no closing quote found")
	}

	// JSON-decode the extracted string (including surrounding quotes).
	jsonStr := script[start : end+1]
	var decoded string
	if err := json.Unmarshal([]byte(jsonStr), &decoded); err != nil {
		return "", fmt.Errorf("json decode RSC string: %w", err)
	}
	return decoded, nil
}

// findEnclosingObject walks backward from pos in s to find the `{` that opens
// the object enclosing the character at pos, at one level above the immediate
// parent. Returns -1 if not found.
func findEnclosingObject(s string, pos int) int {
	// We want the { that is the parent of the object at pos.
	// Strategy: count brace depth going left; when we reach depth -1 we have
	// found the immediate parent {. We then continue to find the grandparent.
	depth := 0
	inString := false
	// Simple reverse scan — does not fully handle escaped quotes inside strings
	// but is sufficient for the well-structured Next.js RSC payload.
	for i := pos - 1; i >= 0; i-- {
		ch := s[i]
		// Detect string boundaries (naively — good enough for RSC JSON).
		if ch == '"' && (i == 0 || s[i-1] != '\\') {
			inString = !inString
		}
		if inString {
			continue
		}
		switch ch {
		case '}':
			depth++
		case '{':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}

// findMatchingBrace returns the index of the closing `}` that matches the
// opening `{` at position 0 of s. Returns -1 if no match is found.
func findMatchingBrace(s string) int {
	depth := 0
	inString := false
	escaped := false

	for i, ch := range s {
		if escaped {
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			if inString {
				escaped = true
			}
		case '"':
			inString = !inString
		case '{':
			if !inString {
				depth++
			}
		case '}':
			if !inString {
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return -1
}
