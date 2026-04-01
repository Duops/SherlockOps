package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/shchepetkov/sherlockops/internal/domain"
	"github.com/shchepetkov/sherlockops/internal/metrics"
)

// ErrQueueFull is returned by Submit when the worker queue is at capacity.
var ErrQueueFull = errors.New("worker queue is full")

// WorkerPool processes alerts asynchronously using a fixed number of workers.
type WorkerPool struct {
	queue    chan *domain.Alert
	pipeline *Pipeline
	workers  int
	logger   *slog.Logger
	wg       sync.WaitGroup
}

// NewWorkerPool creates a WorkerPool with the given pipeline, worker count, and queue capacity.
func NewWorkerPool(pipeline *Pipeline, workers, queueSize int, logger *slog.Logger) *WorkerPool {
	if workers <= 0 {
		workers = 5
	}
	if queueSize <= 0 {
		queueSize = 1000
	}
	return &WorkerPool{
		queue:    make(chan *domain.Alert, queueSize),
		pipeline: pipeline,
		workers:  workers,
		logger:   logger,
	}
}

// Start launches all workers. Each worker reads from the queue until the context
// is cancelled or Stop is called.
func (wp *WorkerPool) Start(ctx context.Context) {
	for i := 0; i < wp.workers; i++ {
		wp.wg.Add(1)
		go wp.run(ctx, i)
	}
	wp.logger.Info("worker pool started", "workers", wp.workers, "queue_size", cap(wp.queue))
}

// Submit enqueues an alert for asynchronous processing.
// It returns ErrQueueFull if the queue is at capacity (non-blocking).
func (wp *WorkerPool) Submit(alert *domain.Alert) error {
	select {
	case wp.queue <- alert:
		metrics.QueueDepth.Set(float64(len(wp.queue)))
		return nil
	default:
		return ErrQueueFull
	}
}

// Stop closes the queue channel and waits for all workers to finish processing
// their current alerts.
func (wp *WorkerPool) Stop() {
	close(wp.queue)
	wp.wg.Wait()
	wp.logger.Info("worker pool stopped")
}

func (wp *WorkerPool) run(ctx context.Context, id int) {
	defer wp.wg.Done()
	wp.logger.Debug("worker started", "worker_id", id)

	for alert := range wp.queue {
		metrics.QueueDepth.Set(float64(len(wp.queue)))
		metrics.ActiveWorkers.Inc()

		if err := wp.pipeline.Process(ctx, alert); err != nil {
			wp.logger.Error("pipeline processing failed",
				"worker_id", id,
				"alert", alert.Name,
				"fingerprint", alert.Fingerprint,
				"error", err,
			)
		}

		metrics.ActiveWorkers.Dec()
	}

	wp.logger.Debug("worker stopped", "worker_id", id)
}
