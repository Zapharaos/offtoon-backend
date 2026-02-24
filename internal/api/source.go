package api

import "fmt"

// Source identifies a known API client by name.
// It is used in search requests to select which clients to query.
type Source string

const (
	SourceAsura Source = "asura"
	SourceNato  Source = "nato"
)

// allSources is the authoritative list of valid sources.
var allSources = []Source{SourceAsura, SourceNato}

// Valid reports whether s is a known source.
func (s Source) Valid() bool {
	for _, known := range allSources {
		if s == known {
			return true
		}
	}
	return false
}

// CustomURL pairs a custom base URL with the source it belongs to.
// The URL is prepended to that client's normal URL list so it is tried first.
type CustomURL struct {
	Source Source `json:"source"`
	URL    string `json:"url"`
}

// Validate checks that the source is known and the URL is non-empty.
func (c CustomURL) Validate() error {
	if !c.Source.Valid() {
		return fmt.Errorf("unknown source %q in custom_urls", c.Source)
	}
	if c.URL == "" {
		return fmt.Errorf("custom_urls entry for source %q has an empty url", c.Source)
	}
	return nil
}
