package pipeline

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// atomicCountAnalyzer wraps an analyzer and counts calls atomically.
type atomicCountAnalyzer struct {
	inner   domain.Analyzer
	counter atomic.Int64
}

func (a *atomicCountAnalyzer) Analyze(ctx context.Context, alert *domain.Alert) (*domain.AnalysisResult, error) {
	a.counter.Add(1)
	return a.inner.Analyze(ctx, alert)
}

func TestWorkerPool_ProcessesAlerts(t *testing.T) {
	mc := newMockCache()
	inner := &mockAnalyzer{
		result: &domain.AnalysisResult{Text: "done"},
	}
	ma := &atomicCountAnalyzer{inner: inner}

	logger := slog.Default()
	pipe := New(mc, ma, []domain.Messenger{&mockMessenger{name: "test"}}, logger)
	wp := NewWorkerPool(pipe, 3, 100, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wp.Start(ctx)

	// Submit 10 alerts.
	for i := 0; i < 10; i++ {
		alert := &domain.Alert{
			Name:   "test-alert",
			Status: domain.StatusFiring,
			Labels: map[string]string{"env": "test", "i": string(rune('0' + i))},
		}
		if err := wp.Submit(alert); err != nil {
			t.Fatalf("submit failed: %v", err)
		}
	}

	wp.Stop()

	if got := ma.counter.Load(); got != 10 {
		t.Errorf("expected 10 processed alerts, got %d", got)
	}
}

func TestWorkerPool_SubmitReturnsErrorWhenQueueFull(t *testing.T) {
	logger := slog.Default()
	pipe := New(newMockCache(), &mockAnalyzer{result: &domain.AnalysisResult{Text: "x"}}, nil, logger)
	wp := NewWorkerPool(pipe, 1, 2, logger)

	// Do not start workers so queue fills up.
	alert := &domain.Alert{Name: "a", Status: domain.StatusFiring, Labels: map[string]string{}}

	// Fill the queue (size 2).
	if err := wp.Submit(alert); err != nil {
		t.Fatalf("first submit should succeed: %v", err)
	}
	if err := wp.Submit(alert); err != nil {
		t.Fatalf("second submit should succeed: %v", err)
	}

	// Third should fail.
	if err := wp.Submit(alert); err != ErrQueueFull {
		t.Errorf("expected ErrQueueFull, got %v", err)
	}

	// Drain manually so Stop doesn't block forever.
	go func() {
		for range wp.queue {
		}
	}()
	wp.Stop()
}

func TestWorkerPool_DefaultValues(t *testing.T) {
	logger := slog.Default()
	pipe := New(newMockCache(), &mockAnalyzer{result: &domain.AnalysisResult{Text: "x"}}, nil, logger)

	wp := NewWorkerPool(pipe, 0, 0, logger)
	if wp.workers != 5 {
		t.Errorf("expected default workers=5, got %d", wp.workers)
	}
	if cap(wp.queue) != 1000 {
		t.Errorf("expected default queue_size=1000, got %d", cap(wp.queue))
	}

	// Clean up.
	go func() {
		for range wp.queue {
		}
	}()
	wp.Stop()
}
