package webtoons

import "testing"

func TestParseToonID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		want    toonRef
		wantErr bool
	}{
		{
			name: "canonical",
			id:   "fantasy_the-ember-knight_2886",
			want: toonRef{Section: "fantasy", Slug: "the-ember-knight", TitleNo: "2886"},
		},
		{
			name: "canvas",
			id:   "canvas_tower-of-god-no-mans-tower_726081",
			want: toonRef{Section: "canvas", Slug: "tower-of-god-no-mans-tower", TitleNo: "726081"},
		},
		{
			name: "section containing a dash",
			id:   "slice-of-life_some-toon_1234",
			want: toonRef{Section: "slice-of-life", Slug: "some-toon", TitleNo: "1234"},
		},
		{
			// Only the outermost separators are split on, so a slug carrying one
			// cannot shift the section or the title_no.
			name: "separator inside the slug",
			id:   "fantasy_odd_slug_here_99",
			want: toonRef{Section: "fantasy", Slug: "odd_slug_here", TitleNo: "99"},
		},
		{
			name: "legacy slash separator still parses",
			id:   "fantasy/tower-of-god/95",
			want: toonRef{Section: "fantasy", Slug: "tower-of-god", TitleNo: "95"},
		},
		{
			name: "bare title_no uses placeholders",
			id:   "2886",
			want: toonRef{Section: placeholderSection, Slug: placeholderSlug, TitleNo: "2886"},
		},
		{
			name: "surrounding separators are trimmed",
			id:   "  _fantasy_tower-of-god_95_ ",
			want: toonRef{Section: "fantasy", Slug: "tower-of-god", TitleNo: "95"},
		},
		{name: "empty", id: "", wantErr: true},
		{name: "separators only", id: "___", wantErr: true},
		{name: "non numeric title_no", id: "fantasy_tower-of-god_abc", wantErr: true},
		{name: "two parts only", id: "fantasy_95", wantErr: true},
		{name: "bare non numeric", id: "tower-of-god", wantErr: true},
		{name: "empty section", id: "_slug_95", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseToonID(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseToonID(%q) = %+v, want an error", tt.id, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseToonID(%q) returned an unexpected error: %v", tt.id, err)
			}
			if got != tt.want {
				t.Fatalf("parseToonID(%q) = %+v, want %+v", tt.id, got, tt.want)
			}
		})
	}
}

// A composite ID must survive a round trip, because it is handed back to us as
// the slug on every later fetch and download.
func TestToonRefStringRoundTrip(t *testing.T) {
	refs := []toonRef{
		{Section: "fantasy", Slug: "the-ember-knight", TitleNo: "2886"},
		{Section: "canvas", Slug: "some-canvas-toon", TitleNo: "726081"},
		{Section: "slice-of-life", Slug: "a-b-c", TitleNo: "1"},
	}

	for _, ref := range refs {
		id := ref.String()
		got, err := parseToonID(id)
		if err != nil {
			t.Fatalf("parseToonID(%q) returned an unexpected error: %v", id, err)
		}
		if got != ref {
			t.Fatalf("round trip of %+v through %q gave %+v", ref, id, got)
		}
	}
}

// The ID is used verbatim as a directory name by the archive writer and by the
// browser's File System Access API, which rejects any name containing a slash.
func TestToonRefStringIsPathSafe(t *testing.T) {
	id := toonRef{Section: "fantasy", Slug: "the-ember-knight", TitleNo: "2886"}.String()
	for _, bad := range []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"} {
		if contains(id, bad) {
			t.Fatalf("composite ID %q contains %q, which is not usable in a directory name", id, bad)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
