package analyzer

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

type slowAnalyzer struct {
	delay      time.Duration
	concurrent atomic.Int64
	maxSeen    atomic.Int64
}

func (s *slowAnalyzer) Analyze(ctx context.Context, alert *domain.Alert) (*domain.AnalysisResult, error) {
	cur := s.concurrent.Add(1)
	defer s.concurrent.Add(-1)

	// Track maximum concurrency observed.
	for {
		old := s.maxSeen.Load()
		if cur <= old || s.maxSeen.CompareAndSwap(old, cur) {
			break
		}
	}

	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return &domain.AnalysisResult{
		AlertFingerprint: alert.Fingerprint,
		Text:             "analysis done",
	}, nil
}

func TestRateLimitedAnalyzer_LimitsConcurrency(t *testing.T) {
	inner := &slowAnalyzer{delay: 50 * time.Millisecond}
	limited := NewRateLimitedAnalyzer(inner, 2, slog.Default())

	ctx := context.Background()
	done := make(chan struct{})

	// Launch 5 concurrent analyses.
	for i := 0; i < 5; i++ {
		go func() {
			alert := &domain.Alert{
				Fingerprint: "fp",
				Name:        "test",
				Status:      domain.StatusFiring,
			}
			_, _ = limited.Analyze(ctx, alert)
			done <- struct{}{}
		}()
	}

	for i := 0; i < 5; i++ {
		<-done
	}

	if max := inner.maxSeen.Load(); max > 2 {
		t.Errorf("expected max concurrency 2, observed %d", max)
	}
}

func TestRateLimitedAnalyzer_ReturnsErrorOnCancelledContext(t *testing.T) {
	// Inner analyzer that blocks forever.
	inner := &slowAnalyzer{delay: 10 * time.Second}
	limited := NewRateLimitedAnalyzer(inner, 1, slog.Default())

	ctx := context.Background()

	// Fill the semaphore.
	go func() {
		alert := &domain.Alert{Fingerprint: "fp1", Name: "blocker", Status: domain.StatusFiring}
		_, _ = limited.Analyze(ctx, alert)
	}()

	// Give the goroutine a moment to acquire the semaphore.
	time.Sleep(10 * time.Millisecond)

	// Now try with a cancelled context.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	alert := &domain.Alert{Fingerprint: "fp2", Name: "test", Status: domain.StatusFiring}
	_, err := limited.Analyze(cancelCtx, alert)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func TestRateLimitedAnalyzer_DefaultMaxConcurrent(t *testing.T) {
	inner := &slowAnalyzer{delay: 0}
	limited := NewRateLimitedAnalyzer(inner, 0, slog.Default())
	if cap(limited.sem) != 3 {
		t.Errorf("expected default maxConcurrent=3, got %d", cap(limited.sem))
	}
}
