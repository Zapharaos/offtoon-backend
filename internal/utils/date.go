package utils

import (
	"regexp"
	"time"

	"github.com/spf13/viper"
	"go.uber.org/zap"
)

var timeLocation *time.Location

// ordinalSuffixRe strips ordinal suffixes (st, nd, rd, th) from day numbers.
var ordinalSuffixRe = regexp.MustCompile(`(\d+)(st|nd|rd|th)`)

// ParseAsuraDate parses dates in the format used by Asura: "February 24th 2026".
// The ordinal suffix (st/nd/rd/th) is stripped before parsing.
// Returns nil if the input cannot be parsed.
func ParseAsuraDate(raw string) *time.Time {
	// Normalise "24th" → "24", "1st" → "1", etc.
	normalised := ordinalSuffixRe.ReplaceAllString(raw, "$1")
	t, err := time.Parse("January 2 2006", normalised)
	if err != nil {
		return nil
	}
	return &t
}

func InitDate() {
	tz := viper.GetString("timezone")
	if tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			zap.L().Error("Failed to load timezone", zap.String("timezone", tz), zap.Error(err))
		}
		timeLocation = loc
		zap.L().Info("Timezone loaded", zap.String("timezone", tz))
	} else {
		zap.L().Warn("No timezone provided")
	}
}

func GetNowTZ() time.Time {
	if timeLocation == nil {
		return time.Now()
	}
	return time.Now().In(timeLocation)
}

func GetTZ() *time.Location {
	if timeLocation == nil {
		return time.UTC
	}
	return timeLocation
}

// ConvertToServerTZ converts a time from UTC to the server's timezone
func ConvertToServerTZ(t time.Time) time.Time {
	if timeLocation == nil {
		return t
	}
	return t.In(timeLocation)
}
