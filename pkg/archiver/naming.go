package archiver

import "strings"

// maxSeriesNameRunes caps the series name so that the archive path stays well
// clear of the 260-character limit Windows still enforces on extraction, once
// the user's own download directory and the chapter file name are added to it.
const maxSeriesNameRunes = 80

// fallbackSeriesName is used when neither the title nor the slug survives
// sanitising, so an archive always has a name.
const fallbackSeriesName = "toon"

// reservedWindowsNames cannot be used as a file name on Windows, with or
// without an extension: "CON.zip" is refused just like "CON".
var reservedWindowsNames = map[string]struct{}{
	"con": {}, "prn": {}, "aux": {}, "nul": {},
	"com1": {}, "com2": {}, "com3": {}, "com4": {}, "com5": {},
	"com6": {}, "com7": {}, "com8": {}, "com9": {},
	"lpt1": {}, "lpt2": {}, "lpt3": {}, "lpt4": {}, "lpt5": {},
	"lpt6": {}, "lpt7": {}, "lpt8": {}, "lpt9": {},
}

// SeriesName returns the human-facing name of a download: it is used both for
// the archive file handed to the browser and for the top-level directory inside
// it, so the two always agree.
//
// The slug is a technical identifier. On some sources it happens to read well,
// but on others it carries routing data a reader should never see — WEBTOON's
// is "fantasy_the-ember-knight_2886". The title is what the user recognises, so
// it is preferred whenever the caller supplied one and it survives sanitising;
// the slug remains the fallback for callers that pass no metadata.
func SeriesName(meta *ToonMeta, slug string) string {
	if meta != nil {
		if name := sanitiseFileName(meta.Title); name != "" {
			return name
		}
	}
	if name := sanitiseFileName(slug); name != "" {
		return name
	}
	return fallbackSeriesName
}

// sanitiseFileName removes everything that cannot appear in a file name on the
// platforms an archive may be extracted on, Windows being the strictest of
// them. It returns "" when nothing usable is left.
func sanitiseFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f:
			// Control characters. A tab or a newline separates words, so they
			// become a space rather than vanishing — dropping them outright
			// would weld the surrounding words together.
			b.WriteRune(' ')
		case strings.ContainsRune(`/\:*?"<>|`, r):
			// Reserved on Windows; "/" and "\" would also silently introduce a
			// directory level in the archive.
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}

	// Collapse the runs of whitespace the substitutions may have left behind.
	name := strings.Join(strings.Fields(b.String()), " ")

	// Truncate by runes, never by bytes, so a multi-byte character is not cut
	// in half into invalid UTF-8.
	if runes := []rune(name); len(runes) > maxSeriesNameRunes {
		name = string(runes[:maxSeriesNameRunes])
	}

	// Windows rejects names ending in a dot or a space, and truncating above
	// may have exposed one.
	name = strings.TrimRight(name, " .")

	if _, reserved := reservedWindowsNames[strings.ToLower(name)]; reserved {
		return ""
	}
	return name
}
