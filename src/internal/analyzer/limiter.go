package analyzer

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// RateLimitedAnalyzer wraps a domain.Analyzer with a concurrency semaphore
// to limit the number of parallel Analyze calls.
type RateLimitedAnalyzer struct {
	inner  domain.Analyzer
	sem    chan struct{}
	logger *slog.Logger
}

// NewRateLimitedAnalyzer creates a new RateLimitedAnalyzer that allows at most
// maxConcurrent simultaneous Analyze calls to the inner analyzer.
func NewRateLimitedAnalyzer(inner domain.Analyzer, maxConcurrent int, logger *slog.Logger) *RateLimitedAnalyzer {
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	return &RateLimitedAnalyzer{
		inner:  inner,
		sem:    make(chan struct{}, maxConcurrent),
		logger: logger,
	}
}

// Analyze acquires a semaphore slot before delegating to the inner analyzer.
// If the context is cancelled while waiting for a slot, it returns an error.
func (r *RateLimitedAnalyzer) Analyze(ctx context.Context, alert *domain.Alert) (*domain.AnalysisResult, error) {
	select {
	case r.sem <- struct{}{}:
		// Acquired slot.
	case <-ctx.Done():
		return nil, fmt.Errorf("rate limiter: context cancelled while waiting for semaphore: %w", ctx.Err())
	}

	defer func() { <-r.sem }()

	r.logger.Debug("semaphore acquired, analyzing",
		"fingerprint", alert.Fingerprint,
		"in_use", len(r.sem),
		"capacity", cap(r.sem),
	)

	return r.inner.Analyze(ctx, alert)
}
