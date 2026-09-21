package webtoons

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"golang.org/x/net/html"
)

// Search returns series whose title matches query across all configured URLs.
//
// Request: GET {baseURL}/{lang}/search?keyword={query}
//
// The "ALL" search page already contains both the ORIGINALS and the CANVAS
// section, so a single request covers the whole catalogue.  WEBTOON caps how
// many cards that page renders and offers no pagination on it, so the result
// set is the site's own top matches rather than an exhaustive list.
func (c *Client) Search(ctx context.Context, query string) ([]toon.SearchResult, error) {
	var results []toon.SearchResult
	err := c.TryURLs(ctx, func(baseURL string) (bool, error) {
		res, err := c.searchFromURL(ctx, baseURL, query)
		if err != nil {
			return false, err
		}
		if len(res) == 0 {
			return false, nil
		}
		results = res
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (c *Client) searchFromURL(ctx context.Context, baseURL, query string) ([]toon.SearchResult, error) {
	reqURL := fmt.Sprintf("%s/%s/search?keyword=%s",
		strings.TrimRight(baseURL, "/"), c.language, url.QueryEscape(query))

	doc, _, found, err := c.doHTML(ctx, reqURL)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}

	// Every result — Originals and Canvas alike — is an <a class="link _card_item">.
	cards := findAll(doc, all(hasTag("a"), hasClass("_card_item")))

	var out []toon.SearchResult
	seen := make(map[string]struct{}, len(cards))
	for _, card := range cards {
		res, ok := mapSearchResult(card)
		if !ok {
			continue
		}
		if _, dup := seen[res.ID]; dup {
			continue
		}
		seen[res.ID] = struct{}{}
		out = append(out, res)
	}
	return out, nil
}

// mapSearchResult turns one search card into a toon.SearchResult.
// It reports false when the card carries no resolvable series reference.
func mapSearchResult(card *html.Node) (toon.SearchResult, bool) {
	href := absURL(attr(card, "href"))

	ref, ok := refFromURL(href)
	if !ok {
		return toon.SearchResult{}, false
	}
	// data-title-no is the authoritative id when the href is unusual.
	if no := attr(card, "data-title-no"); isDigits(no) {
		ref.TitleNo = no
	}

	title := textOfClass(card, "title")
	if title == "" {
		return toon.SearchResult{}, false
	}

	// Status, chapter count and rating are not exposed on the search page;
	// they are filled in by Fetch once a series is opened.
	return toon.SearchResult{
		ID:        ref.String(),
		Title:     title,
		CoverURL:  browserSafeImageURL(imgSrc(card)),
		Status:    toon.StatusUnknown,
		Source:    toon.Source(Name),
		SourceURL: href,
	}, true
}
