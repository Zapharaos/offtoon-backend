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
		if !strings.Contains(script, `"pages"`) || !strings.Contains(script, `__next_f`) {
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
func extractRSCChapterData(script string) (*rscChapterData, error) {
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
		// Try alternative: just find the nearest `{` that contains "chapter"
		chapterKey = `"chapter":{`
		chapterIdx = strings.LastIndex(script[:pagesIdx], chapterKey)
	}
	if chapterIdx < 0 {
		return nil, nil
	}

	// Step back to include the enclosing `{` of the outer object
	// (which also has "comic": {...}).
	// Look for the `{"comic":` or just walk left for the first `{` before
	// `"chapter":`.
	outerStart := strings.LastIndex(script[:chapterIdx], `{"comic":`)
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

	// The raw string may contain JSON-escaped sequences (e.g. `\u003c` for `<`,
	// `\u0026` for `&`) because it was double-encoded inside the outer string.
	// json.Unmarshal handles Unicode escapes natively, so we can unmarshal directly.
	var data rscChapterData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("unmarshal RSC chapter data: %w", err)
	}

	if len(data.Chapter.Pages) == 0 {
		return nil, nil
	}

	return &data, nil
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
