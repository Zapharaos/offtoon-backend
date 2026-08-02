package archiver

import (
	"encoding/json"
	"fmt"
	"time"
)

// ToonMeta carries series-level metadata written into the manifest.json of a
// FormatOfftoon archive. For all other formats it is ignored and may be nil.
type ToonMeta struct {
	Title       string   `json:"title"`
	Author      string   `json:"author"`
	Artist      string   `json:"artist,omitempty"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Rating      float64  `json:"rating,omitempty"`
	Source      string   `json:"source,omitempty"`
	SourceURL   string   `json:"source_url,omitempty"`
	CoverURL    string   `json:"cover_url,omitempty"`
}

// offtoonManifest is serialised as manifest.json at the root of the archive.
type offtoonManifest struct {
	SpecVersion  int               `json:"spec_version"`
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Author       string            `json:"author"`
	Artist       string            `json:"artist,omitempty"`
	Description  string            `json:"description,omitempty"`
	Status       string            `json:"status,omitempty"`
	Genres       []string          `json:"genres,omitempty"`
	Rating       float64           `json:"rating,omitempty"`
	Source       string            `json:"source,omitempty"`
	SourceURL    string            `json:"source_url,omitempty"`
	Cover        string            `json:"cover,omitempty"` // "cover.webp" when present
	DownloadedAt time.Time         `json:"downloaded_at"`
	Chapters     []manifestChapter `json:"chapters"`
}

// manifestChapter is one chapter entry inside manifest.json.
type manifestChapter struct {
	ID     string  `json:"id"`
	Number float64 `json:"number"`
	Title  string  `json:"title"`
	Pages  int     `json:"pages"`
	Path   string  `json:"path"`   // e.g. "chapters/001"
	Status string  `json:"status"` // "success" | "incomplete" | "failed"
}

// buildOfftoonManifest serialises the manifest to pretty-printed JSON.
func buildOfftoonManifest(slug string, meta *ToonMeta, chapters []manifestChapter, hasCover bool) ([]byte, error) {
	m := offtoonManifest{
		SpecVersion:  1,
		ID:           slug,
		DownloadedAt: time.Now().UTC(),
		Chapters:     chapters,
	}
	if meta != nil {
		m.Title = meta.Title
		m.Author = meta.Author
		m.Artist = meta.Artist
		m.Description = meta.Description
		m.Status = meta.Status
		m.Genres = meta.Genres
		m.Rating = meta.Rating
		m.Source = meta.Source
		m.SourceURL = meta.SourceURL
	}
	if hasCover {
		m.Cover = "cover.webp"
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("build manifest: %w", err)
	}
	return b, nil
}

// safeChapterDir returns the directory path for a chapter inside the archive,
// e.g. "chapters/001" or "chapters/074.5".
func safeChapterDir(num float64) string {
	if num == float64(int(num)) {
		return fmt.Sprintf("chapters/%03.0f", num)
	}
	return fmt.Sprintf("chapters/%06.1f", num)
}
