package webtoons

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
)

// The mobile site backs its episode list with a JSON endpoint that the desktop
// site has no equivalent for.  It matters a great deal here: the HTML list
// renders only nine episodes per page, so walking a 250-episode series costs ~28
// throttled requests, while this endpoint returns the whole thing in one call.
// Sending that many requests per opened series is also what got us
// connection-reset by the edge, so the fast path is a politeness fix as much as
// a latency one.
const (
	// mobileAPIBaseURL hosts the JSON episode list. It is a fixed internal
	// endpoint rather than a configured mirror, so it is not part of URLs().
	mobileAPIBaseURL = "https://m.webtoons.com"

	// episodeAPIPageSize is large enough to return even the longest series in a
	// single response (Tower of God's 652 episodes come back in one call).
	// The cursor loop below still handles a server-side cap being reintroduced.
	episodeAPIPageSize = 1000

	// maxEpisodeAPIPages caps the cursor loop so a misbehaving cursor cannot
	// turn into an unbounded crawl.
	maxEpisodeAPIPages = 20
)

// apiEpisode is one entry of the mobile episode list.
//
// The endpoint exposes no paid/locked marker, so Fast Pass and Daily Pass
// episodes cannot be filtered out here — they are caught at download time, when
// their viewer page comes back without any image.
type apiEpisode struct {
	EpisodeNo          int    `json:"episodeNo"`
	EpisodeTitle       string `json:"episodeTitle"`
	ViewerLink         string `json:"viewerLink"`
	ExposureDateMillis int64  `json:"exposureDateMillis"`
}

// apiEpisodeListResponse wraps the endpoint's envelope.  A series the endpoint
// does not serve (every Canvas title) answers 200 with a null result rather
// than an HTTP error, so Result must be checked, not just the status code.
type apiEpisodeListResponse struct {
	Result *struct {
		EpisodeList []apiEpisode `json:"episodeList"`
		NextCursor  int          `json:"nextCursor"`
	} `json:"result"`
	Success bool `json:"success"`
}

// canUseEpisodeAPI reports whether the JSON fast path applies to this series.
//
// Two limits rule it out. Canvas titles are simply not served by the endpoint,
// and the endpoint ignores every language hint we can send — it always answers
// with English titles and /en/ viewer links — so a client configured for
// another language has to keep scraping the localised HTML.
func (c *Client) canUseEpisodeAPI(ref toonRef) bool {
	return !ref.isCanvas() && c.language == defaultLanguage
}

// fetchEpisodesAPI returns every episode of a series from the JSON endpoint.
//
// found is false when the endpoint does not serve this series, which is the
// caller's signal to fall back to walking the HTML list.
func (c *Client) fetchEpisodesAPI(ctx context.Context, ref toonRef) (chapters []toon.Chapter, found bool, err error) {
	byID := make(map[string]toon.Chapter)
	cursor := 0

	for page := 0; page < maxEpisodeAPIPages; page++ {
		reqURL := fmt.Sprintf("%s/api/v1/webtoon/%s/episodes?pageSize=%d",
			mobileAPIBaseURL, ref.TitleNo, episodeAPIPageSize)
		if cursor > 0 {
			reqURL += fmt.Sprintf("&cursor=%d", cursor)
		}

		var resp apiEpisodeListResponse
		if err := c.doJSON(ctx, reqURL, &resp); err != nil {
			return nil, false, err
		}
		if resp.Result == nil {
			// Not served here (Canvas, unknown title). Only meaningful on the
			// first call — mid-loop it would mean the series vanished, and
			// falling back re-reads it from HTML either way.
			if page == 0 {
				return nil, false, nil
			}
			break
		}

		before := len(byID)
		for _, e := range resp.Result.EpisodeList {
			ch := mapAPIEpisode(e)
			if _, dup := byID[ch.ID]; !dup {
				byID[ch.ID] = ch
			}
		}

		// A zero cursor marks the end; a stalled one means the server stopped
		// advancing and would otherwise loop forever.
		if resp.Result.NextCursor <= cursor || len(byID) == before {
			break
		}
		cursor = resp.Result.NextCursor
	}

	if len(byID) == 0 {
		return nil, false, nil
	}

	out := make([]toon.Chapter, 0, len(byID))
	for _, ch := range byID {
		out = append(out, ch)
	}
	sortChapters(out)
	return out, true, nil
}

// mapAPIEpisode turns one JSON episode into a toon.Chapter.
func mapAPIEpisode(e apiEpisode) toon.Chapter {
	id := strconv.Itoa(e.EpisodeNo)

	title := e.EpisodeTitle
	if title == "" {
		title = "Episode " + id
	}

	ch := toon.Chapter{
		ID:     id,
		Title:  title,
		Number: float64(e.EpisodeNo),
		URL:    absURL(e.ViewerLink),
	}

	if e.ExposureDateMillis > 0 {
		d := time.UnixMilli(e.ExposureDateMillis).UTC()
		ch.Date = &d
	}

	return ch
}

// doJSON performs a GET request and decodes the JSON response into out.
func (c *Client) doJSON(ctx context.Context, reqURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("%s: build request for %s: %w", Name, reqURL, err)
	}
	req.Header.Set("Accept", "application/json")

	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()

	// Retried like the HTML pages: a dropped connection here would otherwise
	// silently demote the whole series to the slow HTML walk.
	resp, err := c.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("%s: GET %s: %w", Name, reqURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: unexpected status %d for %s", Name, resp.StatusCode, reqURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: read body for %s: %w", Name, reqURL, err)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: decode JSON from %s: %w", Name, reqURL, err)
	}
	return nil
}
