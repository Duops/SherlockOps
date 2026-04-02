package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
	"github.com/Duops/SherlockOps/internal/metrics"
)

// Pipeline orchestrates the alert processing flow: deduplication via cache,
// LLM analysis, and delivery through messengers.
type Pipeline struct {
	cache      domain.Cache
	analyzer   domain.Analyzer
	messengers []domain.Messenger
	logger     *slog.Logger
}

// New creates a Pipeline with the given dependencies.
func New(cache domain.Cache, analyzer domain.Analyzer, messengers []domain.Messenger, logger *slog.Logger) *Pipeline {
	return &Pipeline{
		cache:      cache,
		analyzer:   analyzer,
		messengers: messengers,
		logger:     logger,
	}
}

// Process handles a single alert through the full pipeline.
// It uses two-phase delivery for webhook-originated alerts (no ReplyTarget):
//   - Phase 1: Post the raw alert to messengers immediately, get message refs.
//   - Phase 2: Run AI analysis, then reply in thread (Slack) or edit (TG/Teams).
//
// For bot-originated alerts (ReplyTarget already set), the existing single-phase
// SendAnalysis flow is used.
func (p *Pipeline) Process(ctx context.Context, alert *domain.Alert) error {
	// 1. Compute fingerprint if missing.
	if alert.Fingerprint == "" {
		alert.Fingerprint = domain.Fingerprint(alert.Name, alert.Labels)
	}

	p.logger.Info("processing alert",
		"fingerprint", alert.Fingerprint,
		"name", alert.Name,
		"status", alert.Status,
	)

	// Bot listener mode: alert already has a ReplyTarget, use single-phase flow.
	if alert.ReplyTarget != nil && alert.ReplyTarget.ThreadID != "" {
		return p.processSinglePhase(ctx, alert)
	}

	// Webhook mode: use two-phase delivery.
	return p.processTwoPhase(ctx, alert)
}

// processSinglePhase handles alerts from bot listeners (existing flow).
func (p *Pipeline) processSinglePhase(ctx context.Context, alert *domain.Alert) error {
	// Resolved alerts: mark in cache and return.
	if alert.Status == domain.StatusResolved {
		if err := p.cache.MarkResolved(ctx, alert.Fingerprint, alert.EndsAt); err != nil {
			return fmt.Errorf("pipeline: mark resolved: %w", err)
		}
		p.logger.Info("alert marked resolved", "fingerprint", alert.Fingerprint)
		return nil
	}

	// Check cache (unless reanalyze is requested).
	if alert.UserCommand != "reanalyze" {
		cached, err := p.cache.Get(ctx, alert.Fingerprint)
		if err != nil {
			return fmt.Errorf("pipeline: cache get: %w", err)
		}
		if cached != nil {
			p.logger.Info("cache hit", "fingerprint", alert.Fingerprint)
			metrics.CacheHits.Inc()
			metrics.AlertsAnalyzed.WithLabelValues("cached").Inc()
			return p.send(ctx, alert, cached)
		}
	}
	metrics.CacheMisses.Inc()

	// Analyze via LLM.
	start := time.Now()
	result, err := p.analyzer.Analyze(ctx, alert)
	if err != nil {
		metrics.AlertsAnalyzed.WithLabelValues("error").Inc()
		p.sendError(ctx, alert, err)
		return fmt.Errorf("pipeline: analyze: %w", err)
	}
	duration := time.Since(start).Seconds()

	metrics.AlertsAnalyzed.WithLabelValues("success").Inc()
	metrics.TokensTotal.WithLabelValues("input").Add(float64(result.InputTokens))
	metrics.TokensTotal.WithLabelValues("output").Add(float64(result.OutputTokens))
	metrics.CostTotal.Add(estimateCostValue(result))
	metrics.AnalysisDuration.Observe(duration)
	metrics.AnalysisDurationBySource.WithLabelValues(alert.Source).Observe(duration)
	metrics.AnalysisIterations.Observe(float64(result.Iterations))

	// Store in cache.
	if err := p.cache.Set(ctx, result); err != nil {
		p.logger.Warn("failed to cache result", "error", err)
	}

	return p.send(ctx, alert, result)
}

// processTwoPhase handles webhook-originated alerts with two-phase delivery.
func (p *Pipeline) processTwoPhase(ctx context.Context, alert *domain.Alert) error {
	// 1. Resolved handling.
	if alert.Status == domain.StatusResolved {
		if err := p.cache.MarkResolved(ctx, alert.Fingerprint, alert.EndsAt); err != nil {
			return fmt.Errorf("pipeline: mark resolved: %w", err)
		}
		// Still notify messengers about resolution.
		targets := p.resolveMessengers(alert)
		for _, m := range targets {
			if _, err := m.SendAlert(ctx, alert); err != nil {
				p.logger.Error("phase 1: send resolved alert failed", "messenger", m.Name(), "error", err)
			}
		}
		p.logger.Info("alert marked resolved", "fingerprint", alert.Fingerprint)
		return nil
	}

	// 2. Phase 1: Post alert to all messengers immediately.
	targets := p.resolveMessengers(alert)
	var refs []*domain.MessageRef
	for _, m := range targets {
		ref, err := m.SendAlert(ctx, alert)
		if err != nil {
			p.logger.Error("phase 1: send alert failed", "messenger", m.Name(), "error", err)
			continue
		}
		if ref != nil {
			refs = append(refs, ref)
		}
	}

	// 3. Check cache (skip if reanalyze).
	if alert.UserCommand != "reanalyze" {
		cached, err := p.cache.Get(ctx, alert.Fingerprint)
		if err != nil {
			return fmt.Errorf("pipeline: cache get: %w", err)
		}
		if cached != nil {
			p.logger.Info("cache hit", "fingerprint", alert.Fingerprint)
			metrics.CacheHits.Inc()
			metrics.AlertsAnalyzed.WithLabelValues("cached").Inc()
			for _, ref := range refs {
				m := p.findMessenger(ref.Messenger)
				if m != nil {
					if err := m.SendAnalysisReply(ctx, ref, cached); err != nil {
						p.logger.Error("phase 2: send cached analysis failed", "messenger", m.Name(), "error", err)
						metrics.MessengerDeliveryTotal.WithLabelValues(m.Name(), "error").Inc()
					} else {
						metrics.MessengerDeliveryTotal.WithLabelValues(m.Name(), "success").Inc()
					}
				}
			}
			return nil
		}
	}
	metrics.CacheMisses.Inc()

	// 4. Phase 2: AI analysis.
	start := time.Now()
	result, err := p.analyzer.Analyze(ctx, alert)
	if err != nil {
		metrics.AlertsAnalyzed.WithLabelValues("error").Inc()
		for _, ref := range refs {
			m := p.findMessenger(ref.Messenger)
			if m != nil {
				m.SendError(ctx, alert, err)
			}
		}
		return fmt.Errorf("pipeline: analyze: %w", err)
	}
	duration := time.Since(start).Seconds()

	metrics.AlertsAnalyzed.WithLabelValues("success").Inc()
	metrics.TokensTotal.WithLabelValues("input").Add(float64(result.InputTokens))
	metrics.TokensTotal.WithLabelValues("output").Add(float64(result.OutputTokens))
	metrics.CostTotal.Add(estimateCostValue(result))
	metrics.AnalysisDuration.Observe(duration)
	metrics.AnalysisDurationBySource.WithLabelValues(alert.Source).Observe(duration)
	metrics.AnalysisIterations.Observe(float64(result.Iterations))

	// 5. Cache result.
	if err := p.cache.Set(ctx, result); err != nil {
		p.logger.Warn("failed to cache result", "error", err)
	}

	// 6. Send analysis as reply/edit to all refs.
	for _, ref := range refs {
		m := p.findMessenger(ref.Messenger)
		if m != nil {
			if err := m.SendAnalysisReply(ctx, ref, result); err != nil {
				p.logger.Error("phase 2: send analysis failed", "messenger", m.Name(), "error", err)
				metrics.MessengerDeliveryTotal.WithLabelValues(m.Name(), "error").Inc()
			} else {
				metrics.MessengerDeliveryTotal.WithLabelValues(m.Name(), "success").Inc()
			}
		}
	}

	return nil
}

// send delivers an analysis result to the appropriate messenger(s).
func (p *Pipeline) send(ctx context.Context, alert *domain.Alert, result *domain.AnalysisResult) error {
	targets := p.resolveMessengers(alert)
	if len(targets) == 0 {
		p.logger.Warn("no messenger found for alert", "fingerprint", alert.Fingerprint)
		return nil
	}

	var firstErr error
	for _, m := range targets {
		if err := m.SendAnalysis(ctx, alert, result); err != nil {
			p.logger.Error("send failed", "messenger", m.Name(), "error", err)
			metrics.MessengerDeliveryTotal.WithLabelValues(m.Name(), "error").Inc()
			if firstErr == nil {
				firstErr = fmt.Errorf("pipeline: send via %s: %w", m.Name(), err)
			}
		} else {
			metrics.MessengerDeliveryTotal.WithLabelValues(m.Name(), "success").Inc()
		}
	}
	return firstErr
}

// sendError notifies messenger(s) about a processing error.
func (p *Pipeline) sendError(ctx context.Context, alert *domain.Alert, pipeErr error) {
	for _, m := range p.resolveMessengers(alert) {
		if err := m.SendError(ctx, alert, pipeErr); err != nil {
			p.logger.Error("send error failed", "messenger", m.Name(), "error", err)
			metrics.MessengerDeliveryTotal.WithLabelValues(m.Name(), "error").Inc()
		} else {
			metrics.MessengerDeliveryTotal.WithLabelValues(m.Name(), "success").Inc()
		}
	}
}

// resolveMessengers returns the messenger matching the alert's ReplyTarget,
// or all messengers if no specific target is set.
// resolveMessengers always returns all enabled messengers.
// ReplyTarget is used by each messenger to pick the right channel, not to filter messengers.
func (p *Pipeline) resolveMessengers(_ *domain.Alert) []domain.Messenger {
	return p.messengers
}

// findMessenger returns the messenger with the given name, or nil if not found.
func (p *Pipeline) findMessenger(name string) domain.Messenger {
	for _, m := range p.messengers {
		if m.Name() == name {
			return m
		}
	}
	return nil
}

// knownPricing maps model name prefixes to pricing per 1M tokens (USD).
var knownPricing = map[string]struct{ input, output float64 }{
	"claude-opus":   {15.0, 75.0},
	"claude-sonnet": {3.0, 15.0},
	"claude-haiku":  {0.80, 4.0},
	"gpt-4o":        {2.50, 10.0},
	"gpt-4o-mini":   {0.15, 0.60},
	"gpt-4-turbo":   {10.0, 30.0},
	"gpt-4":         {30.0, 60.0},
	"gpt-3.5":       {0.50, 1.50},
	"deepseek":      {0.27, 1.10},
}

// estimateCostValue returns the estimated cost in USD for an analysis result.
func estimateCostValue(r *domain.AnalysisResult) float64 {
	if r.InputTokens == 0 && r.OutputTokens == 0 {
		return 0
	}
	var inputPrice, outputPrice float64
	if r.InputTokenCost > 0 || r.OutputTokenCost > 0 {
		inputPrice = r.InputTokenCost
		outputPrice = r.OutputTokenCost
	} else {
		model := strings.ToLower(r.Model)
		var bestLen int
		for prefix, p := range knownPricing {
			if strings.HasPrefix(model, prefix) && len(prefix) > bestLen {
				bestLen = len(prefix)
				inputPrice = p.input
				outputPrice = p.output
			}
		}
		if bestLen == 0 {
			return 0
		}
	}
	return float64(r.InputTokens)/1_000_000*inputPrice +
		float64(r.OutputTokens)/1_000_000*outputPrice
}
