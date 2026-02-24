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
	// Set default values for viper
	viper.SetDefault("setruntime.client_chan_cap", 100)
	viper.SetDefault("setruntime.change_chan_cap", 20)
	viper.SetDefault("setruntime.timeout", 30*time.Minute)
	viper.SetDefault("setruntime.client_timeout", 10*time.Minute)
	viper.SetDefault("setruntime.client_timeout_check_freq", 30*time.Second)

	return RuntimeOptions{
		ClientChanCap:          viper.GetInt("setruntime.client_chan_cap"),
		ChangeChanCap:          viper.GetInt("setruntime.change_chan_cap"),
		Timeout:                viper.GetDuration("setruntime.timeout"),
		ClientTimeout:          viper.GetDuration("setruntime.client_timeout"),
		ClientTimeoutCheckFreq: viper.GetDuration("setruntime.client_timeout_check_freq"),
	}
}
