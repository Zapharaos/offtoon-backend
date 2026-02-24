package workerpool

import "time"

// Config contains configuration for creating a worker pool
type Config struct {
	Workers       int           // Number of concurrent workers
	JobBufferSize int           // Size of job channel buffer (0 = unbuffered)
	FlushInterval time.Duration // Interval for flushing results
	BatchSize     int           // Number of items per batch
}

// NewConfig creates a new pool configuration with the given parameters
func NewConfig(workers int, batchSize int) Config {
	return Config{
		Workers:       workers,
		BatchSize:     batchSize,
		JobBufferSize: 0,                      // Default: unbuffered
		FlushInterval: 500 * time.Millisecond, // Default: 500ms
	}
}

// NewConfigOptimal creates a config with optimal values calculated from job count
// activeWp: number of currently active worker pools (-1 to ignore and use regular calculation)
func NewConfigOptimal(jobCount, activeWp int) Config {
	return Config{
		Workers:       CalculateOptimalWorkers(jobCount, activeWp),
		BatchSize:     CalculateOptimalBatchSize(jobCount, activeWp),
		JobBufferSize: 0,
		FlushInterval: 500 * time.Millisecond,
	}
}

// WithWorkers sets the number of workers for the config
func (c Config) WithWorkers(workers int) Config {
	c.Workers = workers
	return c
}

// WithBatchSize sets the batch size for the config
func (c Config) WithBatchSize(batchSize int) Config {
	c.BatchSize = batchSize
	return c
}

// WithJobBufferSize sets the job buffer size for the config
func (c Config) WithJobBufferSize(bufferSize int) Config {
	c.JobBufferSize = bufferSize
	return c
}

// WithFlushInterval sets the flush interval for the config
func (c Config) WithFlushInterval(interval time.Duration) Config {
	c.FlushInterval = interval
	return c
}

// CalculateOptimalWorkers determines the appropriate number of workers based on job count
// This is optimized for Redis I/O-bound operations (network latency is the bottleneck)
// activeWp: number of currently active worker pools (-1 to ignore)
func CalculateOptimalWorkers(jobCount, activeWp int) int {
	// Base calculation based on job count
	var baseWorkers int
	switch {
	case jobCount < 50:
		baseWorkers = 2 // Very small workloads - minimal overhead
	case jobCount < 200:
		baseWorkers = 5 // Small workloads - good parallelism without overwhelming Redis
	case jobCount < 500:
		baseWorkers = 8 // Medium workloads - sweet spot for Redis operations
	case jobCount < 1000:
		baseWorkers = 10 // Large workloads - high parallelism
	case jobCount < 2000:
		baseWorkers = 12 // Very large workloads
	default:
		baseWorkers = 15 // Huge workloads - capped to prevent Redis connection pool exhaustion
	}

	// If activeWp is -1, return base calculation
	if activeWp < 0 {
		return baseWorkers
	}

	// Adjust workers based on active worker pools to prevent resource exhaustion
	// The more active pools, the fewer workers per pool to maintain system stability
	switch {
	case activeWp == 0:
		// No other pools active - can use full base workers
		return baseWorkers
	case activeWp == 1:
		// One other pool - reduce by 15% to share resources
		return max(2, baseWorkers*85/100)
	case activeWp == 2:
		// Two other pools - reduce by 30%
		return max(2, baseWorkers*70/100)
	case activeWp == 3:
		// Three other pools - reduce by 40%
		return max(2, baseWorkers*60/100)
	case activeWp >= 4:
		// Many pools active - reduce by 50% to prevent overload
		return max(2, baseWorkers*50/100)
	default:
		return baseWorkers
	}
}

// max returns the larger of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// CalculateOptimalBatchSize determines the appropriate batch size based on job count
// Balances between frequent progress updates (small batches) and efficiency (large batches)
// activeWp: number of currently active worker pools (-1 to ignore)
func CalculateOptimalBatchSize(jobCount, activeWp int) int {
	// Base calculation based on job count
	var baseBatchSize int
	switch {
	case jobCount < 20:
		baseBatchSize = 5 // Very small - send updates frequently for better UX
	case jobCount < 100:
		baseBatchSize = 10 // Small - good balance
	case jobCount < 500:
		baseBatchSize = 25 // Medium - reduce WebSocket message overhead
	case jobCount < 1000:
		baseBatchSize = 50 // Large - efficient batching
	case jobCount < 5000:
		baseBatchSize = 100 // Very large - minimize overhead
	default:
		baseBatchSize = 200 // Huge - maximum efficiency for bulk operations
	}

	// If activeWp is -1, return base calculation
	if activeWp < 0 {
		return baseBatchSize
	}

	// When multiple pools are active, use smaller batches for better progress granularity
	// This ensures users see updates from all active operations more frequently
	switch {
	case activeWp == 0:
		// No other pools - can use full batch size
		return baseBatchSize
	case activeWp == 1:
		// One other pool - slightly smaller batches for better interleaved updates
		return max(5, baseBatchSize*85/100)
	case activeWp == 2:
		// Two other pools - smaller batches for more frequent updates
		return max(5, baseBatchSize*75/100)
	case activeWp == 3:
		// Three other pools - even smaller batches
		return max(5, baseBatchSize*65/100)
	case activeWp >= 4:
		// Many pools - smallest batches for maximum update frequency
		return max(5, baseBatchSize*50/100)
	default:
		return baseBatchSize
	}
}
