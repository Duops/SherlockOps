// Package review asks the LLM for a periodic noise-reduction review of alert
// volume: which alerts spam, and what to tune, silence or fix to cut them.
package review

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
	"github.com/Duops/SherlockOps/internal/pricing"
)

// ErrAlreadyRunning is returned when a review is requested while one is in progress.
var ErrAlreadyRunning = fmt.Errorf("review already running")

// Reviewer produces and stores alert noise-reduction reviews.
type Reviewer struct {
	llm      domain.LLMProvider
	stats    domain.StatsProvider
	store    domain.ReviewStore
	model    string
	language string
	inCost   float64
	outCost  float64
	logger   *slog.Logger
	running  atomic.Bool
	mu       sync.Mutex
	lastErr  string
}

// New creates a Reviewer. model is used for cost estimation only.
func New(llm domain.LLMProvider, stats domain.StatsProvider, store domain.ReviewStore, model, language string, logger *slog.Logger) *Reviewer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reviewer{llm: llm, stats: stats, store: store, model: model, language: language, logger: logger}
}

// SetTokenCost overrides per-million token prices used for cost estimation.
func (r *Reviewer) SetTokenCost(input, output float64) {
	r.inCost, r.outCost = input, output
}

// LatestReview returns the newest stored review for env ("" = all environments).
func (r *Reviewer) LatestReview(ctx context.Context, env string) (*domain.AlertReview, error) {
	return r.store.LatestReview(ctx, env)
}

// Running reports whether a review is currently being generated.
func (r *Reviewer) Running() bool { return r.running.Load() }

// LastError returns the failure message of the most recent attempt, or "".
func (r *Reviewer) LastError() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

func (r *Reviewer) setLastError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.lastErr = ""
	} else {
		r.lastErr = err.Error()
	}
}

// StartReview generates a review in the background. Only one runs at a time.
func (r *Reviewer) StartReview(env string, window time.Duration) error {
	if !r.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	go func() {
		defer r.running.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_, err := r.run(ctx, env, window)
		r.setLastError(err)
		if err != nil {
			r.logger.Error("alert review failed", "env", env, "error", err)
		}
	}()
	return nil
}

// Run generates and stores a review synchronously.
func (r *Reviewer) Run(ctx context.Context, env string, window time.Duration) (*domain.AlertReview, error) {
	if !r.running.CompareAndSwap(false, true) {
		return nil, ErrAlreadyRunning
	}
	defer r.running.Store(false)
	rev, err := r.run(ctx, env, window)
	r.setLastError(err)
	return rev, err
}

func (r *Reviewer) run(ctx context.Context, env string, window time.Duration) (*domain.AlertReview, error) {
	if window <= 0 {
		window = 7 * 24 * time.Hour
	}
	since := time.Now().Add(-window)
	stats, err := r.stats.AlertStats(ctx, since, env)
	if err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}
	if stats.Total == 0 {
		return nil, fmt.Errorf("no alert notifications in the last %s", window)
	}

	resp, err := r.llm.Chat(ctx, &domain.ChatRequest{
		SystemPrompt: systemPrompt(r.language),
		Messages:     []domain.Message{{Role: "user", Content: BuildPrompt(stats, r.language)}},
		MaxTokens:    4096,
	})
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}
	text := strings.TrimSpace(resp.Content)
	if text == "" {
		return nil, fmt.Errorf("llm returned empty review")
	}

	rev := &domain.AlertReview{
		Environment:  env,
		Since:        since,
		Until:        time.Now(),
		Text:         text,
		Model:        r.model,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
		CostUSD:      pricing.EstimateCost(r.model, resp.InputTokens, resp.OutputTokens, r.inCost, r.outCost),
	}
	if err := r.store.SaveReview(ctx, rev); err != nil {
		return nil, err
	}
	r.logger.Info("alert review generated", "env", env, "window", window.String(),
		"alerts", stats.UniqueAlerts, "notifications", stats.Total, "tokens", resp.InputTokens+resp.OutputTokens)
	return rev, nil
}

// RunLoop generates an all-environments review whenever the latest one is
// older than interval. It checks hourly and blocks until ctx is cancelled.
func (r *Reviewer) RunLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	check := func() {
		latest, err := r.store.LatestReview(ctx, "")
		if err != nil {
			r.logger.Warn("alert review: cannot read latest", "error", err)
			return
		}
		if latest != nil && time.Since(latest.CreatedAt) < interval {
			return
		}
		if _, err := r.Run(ctx, "", interval); err != nil && err != ErrAlreadyRunning {
			r.logger.Warn("scheduled alert review skipped", "error", err)
		}
	}
	check()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
