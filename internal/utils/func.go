package utils

import (
	"fmt"
	"time"

	"go.uber.org/zap"
)

// RunInitWithTime runs a function and logs the time it took to run
func RunInitWithTime(a func(), msg string) {
	zap.L().Info(msg)
	start := time.Now()
	a()
	zap.L().Info(fmt.Sprintf("%s done in %s", msg, time.Since(start)))
}
