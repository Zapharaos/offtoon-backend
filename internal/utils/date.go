package utils

import (
	"time"

	"github.com/spf13/viper"
	"go.uber.org/zap"
)

var timeLocation *time.Location

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
