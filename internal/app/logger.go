package app

import (
	"os"

	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// initLogger initializes the logger.
func initLogger(production bool) zap.Config {
	var zapConfig zap.Config
	encoderConfig := zap.NewProductionEncoderConfig()
	if production {
		zapConfig = zap.NewProductionConfig()
		//encoderConfig = zap.NewProductionEncoderConfig()
	} else {
		zapConfig = zap.NewDevelopmentConfig()
		// not very beautiful, so we use the production encoder config
		//encoderConfig = zap.NewDevelopmentEncoderConfig()
	}

	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	// Create lumberjack syncer for log rotation
	logWriter := getLogWriter(production)

	// Create a custom encoder config
	encoder := zapcore.NewJSONEncoder(encoderConfig)

	// Create a new zap core
	core := zapcore.NewCore(
		encoder,
		logWriter,
		zapConfig.Level,
	)

	// Create the logger
	logger := zap.New(
		core,
		zap.AddCaller(),
		zap.AddStacktrace(zap.ErrorLevel),
	)

	// Replace the global logger
	zap.ReplaceGlobals(logger)

	return zapConfig
}

// getLogWriter returns a zapcore WriteSyncer that uses lumberjack for log rotation
func getLogWriter(production bool) zapcore.WriteSyncer {
	if !production {
		// In development mode, just write to console
		return zapcore.AddSync(os.Stdout)
	}

	// Get log rotation settings from config
	filename := viper.GetString("logger.rotation.filename")
	if filename == "" {
		filename = "logs/offtoon.log" // Default if not specified
	}

	maxSize := viper.GetInt("logger.rotation.max_size")
	if maxSize <= 0 {
		maxSize = 10 // Default 10MB if not specified or invalid
	}

	maxBackups := viper.GetInt("logger.rotation.max_backups")
	if maxBackups <= 0 {
		maxBackups = 5 // Default 5 backups if not specified or invalid
	}

	maxAge := viper.GetInt("logger.rotation.max_age")
	if maxAge <= 0 {
		maxAge = 30 // Default 30 days if not specified or invalid
	}

	// In production mode, use lumberjack for log rotation
	lumberjackLogger := &lumberjack.Logger{
		Filename:   filename,
		MaxSize:    maxSize,                                   // megabytes
		MaxBackups: maxBackups,                                // number of backups
		MaxAge:     maxAge,                                    // days
		Compress:   viper.GetBool("logger.rotation.compress"), // compress or not
	}

	// Return a multi-writer that writes to both stdout and log file
	return zapcore.NewMultiWriteSyncer(
		zapcore.AddSync(os.Stdout),
		zapcore.AddSync(lumberjackLogger),
	)
}
