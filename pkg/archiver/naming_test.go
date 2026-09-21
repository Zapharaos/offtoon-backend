package archiver

import (
	"strings"
	"testing"
)

func TestSeriesName(t *testing.T) {
	tests := []struct {
		name string
		meta *ToonMeta
		slug string
		want string
	}{
		{
			name: "title wins over slug",
			meta: &ToonMeta{Title: "The Ember Knight"},
			slug: "fantasy_the-ember-knight_2886",
			want: "The Ember Knight",
		},
		{
			name: "no metadata falls back to the slug",
			slug: "the-dark-swordsman-returns",
			want: "the-dark-swordsman-returns",
		},
		{
			name: "empty title falls back to the slug",
			meta: &ToonMeta{Title: "   "},
			slug: "fantasy_some-toon_1",
			want: "fantasy_some-toon_1",
		},
		{
			name: "characters illegal on windows are replaced",
			meta: &ToonMeta{Title: `Re:Zero / Season 2 <Director's Cut>`},
			slug: "x",
			want: "Re Zero Season 2 Director's Cut",
		},
		{
			name: "control characters are dropped",
			meta: &ToonMeta{Title: "Tower\tof\nGod"},
			slug: "x",
			want: "Tower of God",
		},
		{
			name: "trailing dot is trimmed",
			meta: &ToonMeta{Title: "Episode 1..."},
			slug: "x",
			want: "Episode 1",
		},
		{
			name: "reserved windows name falls back to the slug",
			meta: &ToonMeta{Title: "CON"},
			slug: "fantasy_con-toon_7",
			want: "fantasy_con-toon_7",
		},
		{
			name: "non-ascii titles are kept",
			meta: &ToonMeta{Title: "Tour de Dieu — Saison 3"},
			slug: "x",
			want: "Tour de Dieu — Saison 3",
		},
		{
			name: "nothing usable anywhere",
			meta: &ToonMeta{Title: "///"},
			slug: "",
			want: fallbackSeriesName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SeriesName(tt.meta, tt.slug); got != tt.want {
				t.Fatalf("SeriesName(%+v, %q) = %q, want %q", tt.meta, tt.slug, got, tt.want)
			}
		})
	}
}

func TestSeriesNameIsAlwaysUsableAsAFileName(t *testing.T) {
	inputs := []string{
		"The Ember Knight",
		`a/b\c:d*e?f"g<h>i|j`,
		strings.Repeat("very long title ", 40),
		"trailing space   ",
		"nul",
		"",
	}

	for _, in := range inputs {
		got := SeriesName(&ToonMeta{Title: in}, "fallback-slug")

		if got == "" {
			t.Fatalf("SeriesName(%q) returned an empty name", in)
		}
		if strings.ContainsAny(got, `/\:*?"<>|`) {
			t.Fatalf("SeriesName(%q) = %q, which contains a reserved character", in, got)
		}
		if strings.HasSuffix(got, " ") || strings.HasSuffix(got, ".") {
			t.Fatalf("SeriesName(%q) = %q, which Windows would reject", in, got)
		}
		if n := len([]rune(got)); n > maxSeriesNameRunes {
			t.Fatalf("SeriesName(%q) returned %d runes, over the %d cap", in, n, maxSeriesNameRunes)
		}
		if _, reserved := reservedWindowsNames[strings.ToLower(got)]; reserved {
			t.Fatalf("SeriesName(%q) = %q, a reserved device name", in, got)
		}
	}
}
