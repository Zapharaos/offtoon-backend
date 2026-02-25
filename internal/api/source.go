package api

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
