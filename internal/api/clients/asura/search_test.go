package asura

import (
	"os"
	"strings"
	"testing"
)

func TestParseSearchPage(t *testing.T) {
	body, err := os.ReadFile(`../../../../tmp/asura_search_response.html`)
	if err != nil {
		t.Skip("search response fixture not found:", err)
	}

	results, _, err := parseSearchPage(body, "https://asuracomic.net")
	if err != nil {
		t.Fatalf("parseSearchPage returned error: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one result, got 0")
	}

	t.Logf("Found %d results", len(results))
	for i, r := range results {
		t.Logf("  [%d] ID=%q Title=%q CoverURL=%v Status=%q LastChapter=%.1f Rating=%.1f SourceURL=%q",
			i+1, r.ID, r.Title, r.CoverURL != "", r.Status, r.LastChapter, r.Rating, r.SourceURL)

		if r.ID == "" {
			t.Error("result has empty ID (slug)")
		}
		if r.Title == "" {
			t.Errorf("result %q has empty title", r.ID)
		}
		if r.CoverURL == "" {
			t.Errorf("result %q has empty cover URL", r.ID)
		}
		if !strings.Contains(r.SourceURL, r.ID) {
			t.Errorf("result %q: SourceURL %q does not contain slug", r.ID, r.SourceURL)
		}
		if r.Source != Name {
			t.Errorf("result %q: Source=%q want %q", r.ID, r.Source, Name)
		}
		if r.Status == "" {
			t.Logf("result %q has empty status (may be absent in older fixture)", r.ID)
		}
		if r.LastChapter == 0 {
			t.Logf("result %q has zero last chapter (may be absent in older fixture)", r.ID)
		}
		if r.Rating == 0 {
			t.Logf("result %q has zero rating (may be absent in older fixture)", r.ID)
		}
		// Slug must be directly usable as FetchParams.Slug
		fp := FetchParams{Slug: r.ID}
		if fp.ClientName() != Name {
			t.Errorf("FetchParams.ClientName()=%q want %q", fp.ClientName(), Name)
		}
	}

	// The fixture is a search for "revenge of the" — this slug must appear.
	const wantSlug = "revenge-of-the-iron-blooded-sword-hound-cd494674"
	found := false
	for _, r := range results {
		if r.ID == wantSlug {
			found = true
			if r.Title == "" {
				t.Errorf("slug %q has empty title", wantSlug)
			}
			if r.CoverURL == "" {
				t.Errorf("slug %q has empty cover URL", wantSlug)
			}
		}
	}
	if !found {
		t.Errorf("expected slug %q in results", wantSlug)
	}

	// No duplicate slugs.
	seen := make(map[string]int)
	for _, r := range results {
		seen[r.ID]++
	}
	for slug, count := range seen {
		if count > 1 {
			t.Errorf("slug %q appears %d times (want 1)", slug, count)
		}
	}
}
