package api

import "github.com/Zapharaos/offtoon-backend/internal/toon"

// Source is the canonical type for identifying an API client.
// It is re-exported from the toon package so that API-layer code can use
// api.Source without importing toon directly.
type Source = toon.Source

const (
	SourceAsura    Source = toon.SourceAsura
	SourceWebtoons Source = toon.SourceWebtoons
)

// allSources is the authoritative list of valid sources.
var allSources = []Source{SourceAsura, SourceWebtoons}

// ValidSource reports whether s is a known source.
func ValidSource(s Source) bool {
	for _, known := range allSources {
		if s == known {
			return true
		}
	}
	return false
}
