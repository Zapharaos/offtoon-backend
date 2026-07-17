package app

import "time"

// Build metadata and process start, exposed for the public health endpoint.
var (
	version   string
	buildDate string
	startedAt = time.Now()
)

// setBuildInfo records build metadata. Called once from Init.
func setBuildInfo(v, b string) {
	version = v
	buildDate = b
}

// Version returns the running binary version (empty if built without it).
func Version() string { return version }

// BuildDate returns the binary build date (empty if built without it).
func BuildDate() string { return buildDate }

// Uptime returns how long the process has been running.
func Uptime() time.Duration { return time.Since(startedAt) }
