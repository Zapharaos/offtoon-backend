package toonruntime

import (
	"time"

	"github.com/spf13/viper"
)

type RuntimeOptions struct {
	ClientChanCap          int
	ChangeChanCap          int
	Timeout                time.Duration
	ClientTimeout          time.Duration
	ClientTimeoutCheckFreq time.Duration
}

// RuntimeOptionsFromConfig creates RuntimeOptions from configuration
func RuntimeOptionsFromConfig() RuntimeOptions {
	// Set default values for viper.
	// Use string literals for durations so viper.GetDuration can parse them
	// correctly. Passing a time.Duration directly stores it as interface{} and
	// comes back as 0 when the yaml key is absent.
	viper.SetDefault("toonruntime.client_chan_cap", 100)
	viper.SetDefault("toonruntime.change_chan_cap", 256)
	viper.SetDefault("toonruntime.timeout", "30m")
	viper.SetDefault("toonruntime.client_timeout", "10m")
	viper.SetDefault("toonruntime.client_timeout_check_freq", "30s")

	return RuntimeOptions{
		ClientChanCap:          viper.GetInt("toonruntime.client_chan_cap"),
		ChangeChanCap:          viper.GetInt("toonruntime.change_chan_cap"),
		Timeout:                viper.GetDuration("toonruntime.timeout"),
		ClientTimeout:          viper.GetDuration("toonruntime.client_timeout"),
		ClientTimeoutCheckFreq: viper.GetDuration("toonruntime.client_timeout_check_freq"),
	}
}
