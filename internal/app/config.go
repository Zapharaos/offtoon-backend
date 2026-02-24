package app

import (
	"log"
	"os"
	"strings"

	"github.com/spf13/viper"
	"go.uber.org/zap"
)

const (
	configPath = "config"
	configName = "config"
	envPrefix  = "OFFTOON"
)

// initializeConfig initialize viper configuration
func initializeConfig() {
	// Load base config (default configuration)
	viper.SetConfigName(configName)
	viper.AddConfigPath(configPath)
	viper.SetConfigType("yaml")

	err := viper.ReadInConfig()
	if err != nil {
		log.Fatalf("Cannot read viper configuration: %s", err)
	}

	// Load production-specific config only if APP_ENV=prod
	env := os.Getenv("APP_ENV")
	if env == "prod" {
		envConfigName := configName + ".prod"
		viper.SetConfigName(envConfigName)
		viper.AddConfigPath(configPath)
		viper.SetConfigType("yaml")

		// Merge production config (non-fatal if not found)
		if err := viper.MergeInConfig(); err != nil {
			if _, ok := err.(viper.ConfigFileNotFoundError); ok {
				log.Printf("Production config file %s.yaml not found, using base config only", envConfigName)
			} else {
				log.Printf("Error reading production config %s.yaml: %s", envConfigName, err)
			}
		} else {
			log.Printf("Loaded production config: %s.yaml", envConfigName)
		}
	} else {
		log.Printf("Using default config.yaml (APP_ENV=%s)", env)
	}

	// Initialize environment variables configuration
	viper.SetEnvPrefix(envPrefix)

	// Replace dots in config keys with underscores for environment variables
	// This allows overriding nested config values with environment variables
	// E.g., checkout.discounts.groups.max_depth becomes BRICK_SCANR_CHECKOUT_DISCOUNTS_GROUPS_MAX_DEPTH
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Enable automatic environment variable binding
	viper.AutomaticEnv()

	// Bind all keys in config to environment variables
	bindEnvs(viper.AllSettings())

	viperDebugFlag := viper.AllSettings()
	delete(viperDebugFlag, "test") // Remove golang standard test flags for cleaner debug
}

// bindEnvs recursively binds each configuration key to its corresponding environment variable
func bindEnvs(settings map[string]interface{}, parentKey ...string) {
	for key, val := range settings {
		// Create the key path
		combinedKey := key
		if len(parentKey) > 0 {
			combinedKey = strings.Join(append(parentKey, key), ".")
		}

		// Bind the combined key to an environment variable
		if err := viper.BindEnv(combinedKey); err != nil {
			zap.L().Error("Failed to bind env var", zap.String("key", combinedKey), zap.Error(err))
		}

		// If this key contains a nested map, recursively bind its keys too
		if nested, ok := val.(map[string]interface{}); ok {
			bindEnvs(nested, append(parentKey, key)...)
		}
	}
}
