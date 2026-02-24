package workerpool

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Pool processes items concurrently using a worker pool
// Supports both batching mode (when batchHandler is provided) and streaming mode (when nil)
type Pool[TJob any, TResult any] struct {
	config         Config
	workerFunc     WorkerFunc[TJob, TResult]
	batchHandler   BatchHandler[TResult]  // Optional: nil for streaming mode
	resultHandler  ResultHandler[TResult] // Optional: for streaming mode
	errorHandler   ErrorHandler
	ctx            context.Context
	cancel         context.CancelFunc
	jobs           chan TJob
	results        chan TResult
	errors         chan error
	collectorDone  chan error
	wg             sync.WaitGroup
	resultsMutex   sync.Mutex
	totalJobs      int
	processedCount int
	currentBatch   []TResult
}

// WorkerFunc processes a job and returns a result
type WorkerFunc[TJob any, TResult any] func(ctx context.Context, job TJob) (TResult, error)

// ErrorHandler handles errors from workers
type ErrorHandler func(err error)

// BatchHandler handles a batch of results (batching mode)
type BatchHandler[TResult any] func(batch []TResult) error

// ResultHandler handles individual results as they arrive (streaming mode)
type ResultHandler[TResult any] func(result TResult) error

// NewPool creates a new worker pool
// Pass batchHandler=nil for streaming mode, or provide a handler for batching mode
func NewPool[TJob any, TResult any](
	ctx context.Context,
	config Config,
	workerFunc WorkerFunc[TJob, TResult],
	batchHandler BatchHandler[TResult], // Optional: pass nil for streaming mode
) *Pool[TJob, TResult] {
	poolCtx, cancel := context.WithCancel(ctx)

	// Set defaults
	if config.Workers < 1 {
		config.Workers = 1
	}

	// Adjust batch configuration based on mode
	if batchHandler != nil {
		// Batching mode: use configured batch settings
		if config.BatchSize < 1 {
			config.BatchSize = 10
		}
		if config.FlushInterval == 0 {
			config.FlushInterval = 500 * time.Millisecond
		}
	} else {
		// Streaming mode: disable batching
		config.BatchSize = 1
		config.FlushInterval = 0
	}

	batchCapacity := config.BatchSize
	if batchCapacity < 1 {
		batchCapacity = 1
	}

	return &Pool[TJob, TResult]{
		config:        config,
		workerFunc:    workerFunc,
		batchHandler:  batchHandler,
		ctx:           poolCtx,
		cancel:        cancel,
		jobs:          make(chan TJob, config.Workers*2),
		results:       make(chan TResult, config.Workers*2),
		errors:        make(chan error, config.Workers),
		collectorDone: make(chan error, 1),
		currentBatch:  make([]TResult, 0, batchCapacity),
	}
}

// SetErrorHandler sets a custom error handler
func (p *Pool[TJob, TResult]) SetErrorHandler(handler ErrorHandler) {
	p.errorHandler = handler
}

// SetResultHandler sets a handler for individual results (streaming mode only)
// Use this when batchHandler is nil to process results as they arrive
func (p *Pool[TJob, TResult]) SetResultHandler(handler ResultHandler[TResult]) {
	p.resultHandler = handler
}

// Process processes all jobs and returns when complete
func (p *Pool[TJob, TResult]) Process(jobs []TJob) error {
	p.totalJobs = len(jobs)

	if p.totalJobs == 0 {
		return nil
	}

	mode := "streaming"
	if p.batchHandler != nil {
		mode = "batching"
	}

	zap.L().Debug("starting worker pool",
		zap.String("mode", mode),
		zap.Int("num_workers", p.config.Workers),
		zap.Int("total_jobs", p.totalJobs),
		zap.Int("batch_size", p.config.BatchSize),
	)

	// Start workers
	for i := 0; i < p.config.Workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	// Start result collector
	go p.collectResults()

	// Submit jobs
	for _, job := range jobs {
		select {
		case <-p.ctx.Done():
			return p.ctx.Err()
		case p.jobs <- job:
		}
	}
	close(p.jobs)

	// Wait for workers
	p.wg.Wait()
	close(p.errors)
	close(p.results)

	// Check for worker errors
	for err := range p.errors {
		if err != nil {
			p.cancel() // Cancel collector on worker error
			return err
		}
	}

	// Wait for collector to finish
	return <-p.collectorDone
}

// worker processes jobs from the jobs channel
func (p *Pool[TJob, TResult]) worker(workerID int) {
	defer p.wg.Done()
	defer func() {
		p.errors <- nil // Signal completion
	}()

	for {
		select {
		case <-p.ctx.Done():
			return

		case job, ok := <-p.jobs:
			if !ok {
				return
			}

			// Process job
			result, err := p.workerFunc(p.ctx, job)
			if err != nil {
				if p.errorHandler != nil {
					p.errorHandler(err)
				}
				// Still send result (may be zero-value) to track progress
				// Batch handler can decide whether to include it
			}

			// Always send result to ensure progress tracking
			select {
			case <-p.ctx.Done():
				return
			case p.results <- result:
			}
		}
	}
}

// collectResults collects results and sends batches or streams them individually
func (p *Pool[TJob, TResult]) collectResults() {
	// Batching mode
	if p.batchHandler != nil {
		p.collectBatched()
		return
	}

	// Streaming mode
	p.collectStreaming()
}

// collectBatched collects results and sends them in batches
func (p *Pool[TJob, TResult]) collectBatched() {
	flushTicker := time.NewTicker(p.config.FlushInterval)
	defer flushTicker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			p.collectorDone <- p.ctx.Err()
			return

		case result, ok := <-p.results:
			if !ok {
				// Channel closed - send final batch if any
				if len(p.currentBatch) > 0 {
					if err := p.batchHandler(p.currentBatch); err != nil {
						p.collectorDone <- err
						return
					}
					p.currentBatch = p.currentBatch[:0]
				}
				p.collectorDone <- nil
				return
			}

			// Add to current batch
			p.currentBatch = append(p.currentBatch, result)
			p.processedCount++

			// Send batch if full
			if len(p.currentBatch) >= p.config.BatchSize {
				if err := p.batchHandler(p.currentBatch); err != nil {
					p.collectorDone <- err
					return
				}
				p.currentBatch = p.currentBatch[:0]
			}

		case <-flushTicker.C:
			// Periodic flush of partial batch
			if len(p.currentBatch) > 0 {
				if err := p.batchHandler(p.currentBatch); err != nil {
					p.collectorDone <- err
					return
				}
				p.currentBatch = p.currentBatch[:0]
			}
		}
	}
}

// collectStreaming collects results and streams them individually
func (p *Pool[TJob, TResult]) collectStreaming() {
	for {
		select {
		case <-p.ctx.Done():
			p.collectorDone <- p.ctx.Err()
			return

		case result, ok := <-p.results:
			if !ok {
				// Channel closed - all results processed
				p.collectorDone <- nil
				return
			}

			// Process result individually
			if p.resultHandler != nil {
				if err := p.resultHandler(result); err != nil {
					p.collectorDone <- err
					return
				}
			}
			p.processedCount++
		}
	}
}

// Cancel cancels the pool
func (p *Pool[TJob, TResult]) Cancel() {
	p.cancel()
}
