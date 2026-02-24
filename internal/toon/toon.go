package toon

// SearchResult is the shared data returned by any client's Search operation.
type SearchResult struct {
	// ID is the source-specific identifier (opaque to the caller).
	ID       string `json:"id"`
	Title    string `json:"title"`
	CoverURL string `json:"cover_url"`
	// Source is the name of the API client that produced this result.
	Source    string `json:"source"`
	SourceURL string `json:"source_url"`
}

// Page is a single image inside a chapter.
type Page struct {
	Number   int    `json:"number"`
	ImageURL string `json:"image_url"`
}

// Chapter holds the metadata and (optionally) the pages of one chapter.
type Chapter struct {
	// ID is the source-specific identifier.
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Number float64 `json:"number"` // float to support "12.5" style chapters
	URL    string  `json:"url"`
	// Pages is populated only when downloading a chapter's images.
	Pages []Page `json:"pages,omitempty"`
}

// Toon is the canonical shared representation of a webtoon / comic.
type Toon struct {
	// ID is the source-specific identifier.
	ID          string `json:"id"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	Description string `json:"description"`
	CoverURL    string `json:"cover_url"`
	Source      string `json:"source"`
	SourceURL   string `json:"source_url"`
	// Chapters is populated only when fetching full toon details.
	Chapters []Chapter `json:"chapters,omitempty"`
}
