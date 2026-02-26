package toon

import "time"

// Status represents the publication status of a toon.
type Status string

const (
	// StatusUnknown is used when the status could not be determined.
	StatusUnknown Status = "unknown"
	// StatusOngoing means the toon is currently being published.
	StatusOngoing Status = "ongoing"
	// StatusCompleted means the toon has finished publication.
	StatusCompleted Status = "completed"
	// StatusHiatus means the toon is temporarily on hold.
	StatusHiatus Status = "hiatus"
	// StatusDropped means the toon has been cancelled.
	StatusDropped Status = "dropped"
	// StatusSeasonEnd means the current season has ended (may continue).
	StatusSeasonEnd Status = "season_end"
)

// ParseStatus normalises a raw status string (as returned by a source) into
// the canonical Status enum value. Unrecognised values map to StatusUnknown.
func ParseStatus(raw string) Status {
	switch raw {
	case "Ongoing":
		return StatusOngoing
	case "Completed":
		return StatusCompleted
	case "Hiatus":
		return StatusHiatus
	case "Dropped":
		return StatusDropped
	case "Season End":
		return StatusSeasonEnd
	default:
		return StatusUnknown
	}
}

// Source identifies a known API client by name.
// It is used in search/fetch/download requests to select which client to query.
type Source string

const (
	SourceAsura Source = "asura"
	SourceNato  Source = "nato"
)

// SearchResult is the shared data returned by any client's Search operation.
type SearchResult struct {
	// ID is the source-specific identifier (opaque to the caller).
	ID       string `json:"id"`
	Title    string `json:"title"`
	CoverURL string `json:"cover_url"`
	// Status is the publication status of the toon.
	Status Status `json:"status,omitempty"`
	// LastChapter is the latest chapter number available, 0 if unknown.
	LastChapter float64 `json:"last_chapter,omitempty"`
	// Rating is the community rating (e.g. 9.3), 0 if unknown.
	Rating float64 `json:"rating,omitempty"`
	// Source is the API client that produced this result.
	Source    Source `json:"source"`
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
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	Number float64    `json:"number"` // float to support "12.5" style chapters
	Date   *time.Time `json:"date,omitempty"`
	URL    string     `json:"url"`
	// Pages is populated only when downloading a chapter's images.
	Pages []Page `json:"pages,omitempty"`
}

// Toon is the canonical shared representation of a webtoon / comic.
type Toon struct {
	// ID is the source-specific identifier.
	ID            string `json:"id"`
	Title         string `json:"title"`
	Author        string `json:"author"`
	Artist        string `json:"artist,omitempty"`
	Serialization string `json:"serialization,omitempty"`
	Description   string `json:"description"`
	// Note holds the studio/publisher blurb that appears before the synopsis
	// (e.g. "[By the studio that brought you <Solo Leveling>...]").
	// It is empty when the source does not provide such a note.
	Note      string     `json:"note,omitempty"`
	CoverURL  string     `json:"cover_url"`
	Status    Status     `json:"status"`
	Type      string     `json:"type"`
	UpdatedOn *time.Time `json:"updated_on,omitempty"`
	Genres    []string   `json:"genres,omitempty"`
	Rating    float64    `json:"rating,omitempty"`
	// Source is the API client that produced this toon.
	Source    Source `json:"source"`
	SourceURL string `json:"source_url"`
	// Chapters is populated only when fetching full toon details.
	Chapters []Chapter `json:"chapters,omitempty"`
}
