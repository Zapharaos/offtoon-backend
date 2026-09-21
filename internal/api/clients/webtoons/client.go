// Package webtoons implements the api.Client interface for www.webtoons.com.
//
// Unlike Asura, WEBTOON exposes no public JSON API, so every operation scrapes
// the server-rendered HTML pages:
//
//	search   → /{lang}/search?keyword=...
//	fetch    → /{lang}/{section}/{slug}/list?title_no=...&page=N
//	download → /{lang}/{section}/{slug}/{episodeSlug}/viewer?title_no=...&episode_no=...
//
// Series identity
//
//	A series is identified by its numeric title_no, but the URL also carries a
//	section (the genre for Originals, the literal "canvas" for Canvas) and a
//	title slug.  Both of those can change over time, so the canonical ID used
//	throughout offtoon is the composite "section_slug_titleNo" and stale values
//	are recovered via altRefs — WEBTOON 301-redirects to the canonical URL as
//	long as the section *kind* (canvas vs. anything else) is right.
package webtoons

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/throttle"
	"github.com/spf13/viper"
	"golang.org/x/net/html"
)

// Name is the unique identifier used for registration and routing.
const Name = "webtoons"

// publicBaseURL is the human-facing site used for SourceURL fields.
const publicBaseURL = "https://www.webtoons.com"

// defaultLanguage is the content language segment used when none is configured.
const defaultLanguage = "en"

// canvasSection is the URL path segment that marks a Canvas (user-submitted)
// series; every other value in that position is an Originals genre.
const canvasSection = "canvas"

// placeholderSection and placeholderSlug are stand-ins used when the real path
// segments are unknown or stale.  WEBTOON ignores them and 301-redirects to the
// canonical URL for the given title_no.
const (
	placeholderSection = "genre"
	placeholderSlug    = "title"
)

// toonIDSeparator joins the three parts of a composite series ID.
//
// The ID is not just an identifier: it ends up in the toon page URL, as the
// directory the archive is written under, and as the OPFS directory name the
// library imports into. That last one is the binding constraint — the browser's
// File System Access API rejects any name containing a slash — so the separator
// must not be "/". WEBTOON's own genres and slugs are [a-z0-9-] only, so an
// underscore never collides with one.
const toonIDSeparator = "_"

// legacyToonIDSeparator is the separator this client first shipped with. It is
// still accepted when parsing so that toon pages bookmarked or stored before
// the change keep resolving.
const legacyToonIDSeparator = "/"

// -----------------------------------------------------------------------
// Client
// -----------------------------------------------------------------------

// Client implements api.Client for www.webtoons.com.
type Client struct {
	*api.BaseClient
	language string

	// sem caps how many requests may be in flight at once. See maxInFlight.
	sem chan struct{}
}

// maxInFlight bounds concurrent requests to WEBTOON.
//
// The throttler paces when a request may *start*, but puts no ceiling on how
// many are outstanding at once. The archiver resolves chapters in parallel —
// one worker per CPU — so every one of them fired a viewer request within the
// same millisecond. Go speaks HTTP/2 to this host, which multiplexes all of
// them onto a single TCP connection, so when WEBTOON stalled that connection
// the whole batch timed out together and a dozen chapters were lost at once.
//
// Two at a time still pipelines page resolution against image downloading,
// while keeping the burst small enough that the connection survives it.
const maxInFlight = 2

// New creates a ready-to-use WEBTOON client whose URL list, content language
// and throttle settings are taken from the application configuration.
func New() (*Client, error) {
	viper.SetDefault("clients.webtoons.urls", []string{publicBaseURL})
	viper.SetDefault("clients.webtoons.language", defaultLanguage)

	urls := viper.GetStringSlice("clients.webtoons.urls")
	if len(urls) == 0 {
		return nil, fmt.Errorf("webtoons: no URLs configured under clients.webtoons.urls")
	}

	lang := viper.GetString("clients.webtoons.language")
	if lang == "" {
		lang = defaultLanguage
	}

	return &Client{
		BaseClient: api.NewBaseClient(Name, urls, throttleConfig()),
		language:   lang,
		sem:        make(chan struct{}, maxInFlight),
	}, nil
}

// acquire blocks until a request slot is free, or until ctx is done.
func (c *Client) acquire(ctx context.Context) error {
	select {
	case c.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release returns a request slot. It must be called once per acquire.
func (c *Client) release() {
	<-c.sem
}

// throttleConfig builds the throttle configuration from viper (clients.webtoons.throttle.*).
//
// The defaults are deliberately slower than Asura's: these are full HTML pages
// served by the origin site rather than a JSON API built to be called.
func throttleConfig() *throttle.Config {
	const prefix = "clients.webtoons.throttle."

	viper.SetDefault(prefix+"delay_min_ms", 400)
	viper.SetDefault(prefix+"delay_max_ms", 1000)
	viper.SetDefault(prefix+"max_requests", 20)
	viper.SetDefault(prefix+"window_seconds", 10)
	viper.SetDefault(prefix+"max_attempts", 3)
	viper.SetDefault(prefix+"initial_backoff_ms", 1000)
	viper.SetDefault(prefix+"max_backoff_ms", 30000)
	viper.SetDefault(prefix+"backoff_multiplier", 2.0)
	viper.SetDefault(prefix+"baseline_response_time_ms", 500)
	viper.SetDefault(prefix+"slow_threshold_multiplier", 3.0)
	viper.SetDefault(prefix+"adaptive_enabled", true)

	return &throttle.Config{
		DelayMinMs:              viper.GetInt(prefix + "delay_min_ms"),
		DelayMaxMs:              viper.GetInt(prefix + "delay_max_ms"),
		MaxRequests:             viper.GetInt(prefix + "max_requests"),
		WindowSeconds:           viper.GetInt(prefix + "window_seconds"),
		MaxAttempts:             viper.GetInt(prefix + "max_attempts"),
		InitialBackoffMs:        viper.GetInt(prefix + "initial_backoff_ms"),
		MaxBackoffMs:            viper.GetInt(prefix + "max_backoff_ms"),
		BackoffMultiplier:       viper.GetFloat64(prefix + "backoff_multiplier"),
		UserAgents:              throttle.GetDefaultUserAgents(),
		BaselineResponseTimeMs:  viper.GetInt(prefix + "baseline_response_time_ms"),
		SlowThresholdMultiplier: viper.GetFloat64(prefix + "slow_threshold_multiplier"),
		AdaptiveEnabled:         viper.GetBool(prefix + "adaptive_enabled"),
	}
}

// -----------------------------------------------------------------------
// Params
// -----------------------------------------------------------------------

// FetchParams carries the data needed to identify a single series on WEBTOON.
type FetchParams struct {
	// Slug is the composite series ID, e.g. "fantasy_tower-of-god_95".
	Slug string
}

func (p FetchParams) ClientName() string { return Name }

// DownloadParams carries the data needed to download episodes from WEBTOON.
type DownloadParams struct {
	// Slug is the composite series ID, e.g. "fantasy_tower-of-god_95".
	Slug string
	// ChapterIDs holds the episode numbers to download; empty means all.
	ChapterIDs []string
}

func (p DownloadParams) ClientName() string { return Name }

// -----------------------------------------------------------------------
// Series reference
// -----------------------------------------------------------------------

// toonRef is the parsed form of a composite series ID.
type toonRef struct {
	// Section is the first path segment after the language: the genre for an
	// Originals series ("fantasy"), or "canvas" for a Canvas series.
	Section string
	// Slug is the title slug ("tower-of-god").
	Slug string
	// TitleNo is the numeric series identifier ("95") — the only part WEBTOON
	// actually resolves on.
	TitleNo string
}

// parseToonID splits a composite "section_slug_titleNo" ID.
//
// The separator this client originally used ("/") is still accepted, so IDs
// stored or bookmarked before the change keep working.
//
// A bare numeric ID is also accepted and treated as an Originals series with
// placeholder path segments, since the redirect fills in the real ones.
func parseToonID(id string) (toonRef, error) {
	id = strings.TrimSpace(id)

	sep := toonIDSeparator
	if strings.Contains(id, legacyToonIDSeparator) {
		sep = legacyToonIDSeparator
	}

	id = strings.Trim(id, sep)
	if id == "" {
		return toonRef{}, fmt.Errorf("%s: empty toon ID", Name)
	}

	malformed := fmt.Errorf("%s: malformed toon ID %q, want section%sslug%stitleNo",
		Name, id, toonIDSeparator, toonIDSeparator)

	// A bare title_no is accepted: the placeholder path segments are enough for
	// WEBTOON to redirect us to the canonical URL.
	first := strings.Index(id, sep)
	if first < 0 {
		if !isDigits(id) {
			return toonRef{}, malformed
		}
		return toonRef{Section: placeholderSection, Slug: placeholderSlug, TitleNo: id}, nil
	}

	// Split on the outermost separators rather than all of them, so a slug that
	// happens to contain one cannot shift the section or the title_no.
	last := strings.LastIndex(id, sep)
	if last == first {
		return toonRef{}, malformed
	}

	ref := toonRef{
		Section: id[:first],
		Slug:    id[first+len(sep) : last],
		TitleNo: id[last+len(sep):],
	}
	if ref.Section == "" || ref.Slug == "" {
		return toonRef{}, malformed
	}
	if !isDigits(ref.TitleNo) {
		return toonRef{}, fmt.Errorf("%s: malformed toon ID %q, title_no %q is not numeric", Name, id, ref.TitleNo)
	}
	return ref, nil
}

// String renders the composite ID.
func (r toonRef) String() string {
	return r.Section + toonIDSeparator + r.Slug + toonIDSeparator + r.TitleNo
}

// isCanvas reports whether this reference points at a Canvas series.
func (r toonRef) isCanvas() bool {
	return strings.EqualFold(r.Section, canvasSection)
}

// altRefs returns the references to try, in order: the one we were given first,
// then the two placeholder forms that let WEBTOON redirect us to the canonical
// URL when the stored genre or slug has since changed.  Canvas is tried before
// Originals for a reference that already looks like Canvas, and vice versa.
func (r toonRef) altRefs() []toonRef {
	canvas := toonRef{Section: canvasSection, Slug: placeholderSlug, TitleNo: r.TitleNo}
	original := toonRef{Section: placeholderSection, Slug: placeholderSlug, TitleNo: r.TitleNo}

	ordered := []toonRef{r, original, canvas}
	if r.isCanvas() {
		ordered = []toonRef{r, canvas, original}
	}

	out := make([]toonRef, 0, len(ordered))
	seen := make(map[string]struct{}, len(ordered))
	for _, ref := range ordered {
		key := ref.String()
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}

// listURL builds the episode-list URL for the given 1-based page.
func (r toonRef) listURL(base, lang string, page int) string {
	u := fmt.Sprintf("%s/%s/%s/%s/list?title_no=%s",
		strings.TrimRight(base, "/"), lang, r.Section, r.Slug, r.TitleNo)
	if page > 1 {
		u += fmt.Sprintf("&page=%d", page)
	}
	return u
}

// viewerURL builds the episode-viewer URL.  The episode slug is a placeholder:
// WEBTOON resolves the episode on episode_no and redirects to the real slug.
func (r toonRef) viewerURL(base, lang, episodeNo string) string {
	return fmt.Sprintf("%s/%s/%s/%s/episode/viewer?title_no=%s&episode_no=%s",
		strings.TrimRight(base, "/"), lang, r.Section, r.Slug, r.TitleNo, episodeNo)
}

// refFromURL extracts a toonRef from a canonical list or viewer URL.
// It reports false when the URL does not carry a usable title_no.
func refFromURL(raw string) (toonRef, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return toonRef{}, false
	}

	titleNo := u.Query().Get("title_no")
	if !isDigits(titleNo) {
		return toonRef{}, false
	}

	// Path is /{lang}/{section}/{slug}/list (or .../{episodeSlug}/viewer).
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 {
		return toonRef{Section: placeholderSection, Slug: placeholderSlug, TitleNo: titleNo}, true
	}
	return toonRef{Section: parts[1], Slug: parts[2], TitleNo: titleNo}, true
}

// -----------------------------------------------------------------------
// HTTP helper
// -----------------------------------------------------------------------

// doHTML performs a GET request and parses the response body as HTML.
//
// It returns found=false on 404 so callers can fall back to another base URL or
// another toonRef.  finalURL is the URL after any redirects, i.e. the canonical
// page address — the archiver reuses it as the CDN Referer, which WEBTOON's
// image hosts require (they answer 403 without one).
func (c *Client) doHTML(ctx context.Context, reqURL string) (doc *html.Node, finalURL string, found bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, "", false, fmt.Errorf("%s: build request for %s: %w", Name, reqURL, err)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", c.language)
	// WEBTOON gates mature series behind an age-confirmation interstitial;
	// setting the cookie up-front keeps us on the real page.
	req.Header.Set("Cookie", "ageGatePass=true; needGDPR=false; needCCPA=false; needCOPPA=false")

	if err := c.acquire(ctx); err != nil {
		return nil, "", false, err
	}
	defer c.release()

	// Retry matters here, and only transport-level failures need it. WEBTOON
	// does not answer 429 when it decides we are asking too often — it drops
	// the connection outright — so without a retry a single reset permanently
	// fails whatever we were doing, which for the download path means losing a
	// whole chapter. Do retries connection errors, 429 and 5xx with backoff,
	// and returns any other status straight away, so a 404 (stale slug, removed
	// series) still short-circuits instead of being retried three times.
	resp, err := c.Do(ctx, req)
	if err != nil {
		return nil, "", false, fmt.Errorf("%s: GET %s: %w", Name, reqURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode == http.StatusNotFound {
		return nil, "", false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("%s: unexpected status %d for %s", Name, resp.StatusCode, reqURL)
	}

	doc, err = html.Parse(resp.Body)
	if err != nil {
		return nil, "", false, fmt.Errorf("%s: parse HTML from %s: %w", Name, reqURL, err)
	}

	finalURL = reqURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return doc, finalURL, true, nil
}

// -----------------------------------------------------------------------
// Mapping helpers
// -----------------------------------------------------------------------

// parseDayInfo maps the "day_info" line of a series page to a toon.Status.
//
// The line reads "COMPLETED" for a finished series and "UP EVERY MONDAY" (or
// similar) while it is running.  The visible text is localised, so the icon
// class is preferred and the text is only a fallback.
func parseDayInfo(n *html.Node) toon.Status {
	if n == nil {
		return toon.StatusUnknown
	}

	switch {
	case findFirst(n, hasClass("txt_ico_completed")) != nil:
		return toon.StatusCompleted
	case findFirst(n, hasClass("txt_ico_hiatus")) != nil:
		return toon.StatusHiatus
	case findFirst(n, hasClass("txt_ico_up")) != nil:
		return toon.StatusOngoing
	}

	switch txt := strings.ToUpper(text(n)); {
	case strings.Contains(txt, "COMPLETED"):
		return toon.StatusCompleted
	case strings.Contains(txt, "HIATUS"):
		return toon.StatusHiatus
	case txt != "":
		return toon.StatusOngoing
	default:
		return toon.StatusUnknown
	}
}

// WEBTOON serves the same images from two hosts, and only one of them is usable
// from a browser pointed at our own origin.
const (
	// hotlinkedImageHost answers 403 unless the Referer is webtoons.com itself.
	hotlinkedImageHost = "webtoon-phinf.pstatic.net"
	// openImageHost serves the very same assets to anyone, no Referer needed.
	openImageHost = "swebtoon-phinf.pstatic.net"
)

// browserSafeImageURL moves an image URL onto the host that does not enforce
// hotlink protection.
//
// This matters for covers and only for covers: they are rendered by the user's
// browser from our origin, so the Referer is ours and the protected host turns
// them away. Chapter pages are not affected — the archiver fetches those server
// side and sends the viewer URL as Referer, which the protected host accepts.
func browserSafeImageURL(u string) string {
	return strings.Replace(u, "://"+hotlinkedImageHost, "://"+openImageHost, 1)
}

// absURL resolves href against the public site when it is a root-relative path.
func absURL(href string) string {
	if href == "" || strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	if !strings.HasPrefix(href, "/") {
		href = "/" + href
	}
	return publicBaseURL + href
}

// isDigits reports whether s is a non-empty run of ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
