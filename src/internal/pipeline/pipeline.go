package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
	"github.com/Duops/SherlockOps/internal/metrics"
	"github.com/Duops/SherlockOps/internal/pricing"
)

// Pipeline orchestrates the alert processing flow: deduplication via cache,
// LLM analysis, and delivery through messengers.
type Pipeline struct {
	cache      domain.Cache
	analyzer   domain.Analyzer
	messengers []domain.Messenger
	logger     *slog.Logger

	// manual-mode dependencies (optional; nil in auto mode)
	pending domain.PendingStore
	mode    string
}

// New creates a Pipeline in auto mode.
func New(cache domain.Cache, analyzer domain.Analyzer, messengers []domain.Messenger, logger *slog.Logger) *Pipeline {
	return &Pipeline{
		cache:      cache,
		analyzer:   analyzer,
		messengers: messengers,
		logger:     logger,
		mode:       "auto",
	}
}

// SetMode switches the pipeline between "auto" and "manual" processing.
// In "manual" mode the pipeline must also have a PendingStore configured
// via SetPendingStore.
func (p *Pipeline) SetMode(mode string) {
	if mode == "" {
		mode = "auto"
	}
	p.mode = mode
}

// SetPendingStore wires the store used to persist raw alerts in manual mode.
func (p *Pipeline) SetPendingStore(s domain.PendingStore) {
	p.pending = s
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
	// (This includes manual-mode "@bot analyze" mentions resolved against pending store.)
	if alert.ReplyTarget != nil && alert.ReplyTarget.ThreadID != "" {
		return p.processSinglePhase(ctx, alert)
	}

	// Manual mode: post the raw alert and remember it; do not run LLM analysis.
	if p.mode == "manual" && p.pending != nil {
		return p.processManual(ctx, alert)
	}

	// Webhook mode: use two-phase delivery.
	return p.processTwoPhase(ctx, alert)
}

// processManual delivers the raw alert via all messengers and persists it under
// each posted message ID so that a future "@bot analyze" reply can recover the
// original alert and trigger analysis on demand.
func (p *Pipeline) processManual(ctx context.Context, alert *domain.Alert) error {
	if alert.Status == domain.StatusResolved {
		if err := p.cache.MarkResolved(ctx, alert.Fingerprint, alert.EndsAt); err != nil {
			return fmt.Errorf("pipeline: mark resolved: %w", err)
		}
		for _, m := range p.resolveMessengers(alert) {
			if _, err := m.SendAlert(ctx, alert); err != nil {
				p.logger.Error("manual: send resolved alert failed", "messenger", m.Name(), "error", err)
			}
		}
		return nil
	}

	targets := p.resolveMessengers(alert)
	if len(targets) == 0 {
		p.logger.Warn("manual: no messengers configured", "fingerprint", alert.Fingerprint)
		return nil
	}

	for _, m := range targets {
		ref, err := m.SendAlert(ctx, alert)
		if err != nil {
			p.logger.Error("manual: send alert failed", "messenger", m.Name(), "error", err)
			metrics.MessengerDeliveryTotal.WithLabelValues(m.Name(), "error").Inc()
			continue
		}
		metrics.MessengerDeliveryTotal.WithLabelValues(m.Name(), "success").Inc()
		if ref == nil || ref.MessageID == "" {
			// Messenger that does not return a ref (e.g. simple webhook) cannot
			// be used to anchor a manual mention; skip persistence.
			continue
		}
		if err := p.pending.SavePending(ctx, ref, alert); err != nil {
			p.logger.Error("manual: save pending failed",
				"messenger", ref.Messenger,
				"channel", ref.Channel,
				"message_id", ref.MessageID,
				"error", err,
			)
		} else {
			p.logger.Info("manual: pending saved",
				"messenger", ref.Messenger,
				"channel", ref.Channel,
				"message_id", ref.MessageID,
				"fingerprint", alert.Fingerprint,
				"name", alert.Name,
			)
		}
	}
	metrics.AlertsAnalyzed.WithLabelValues("manual_pending").Inc()
	return nil
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

	// Synthetic mention alerts ("thread-mention" / "teams-mention") only reach
	// processSinglePhase when the user pinged @bot in a thread BUT we did NOT
	// find a matching pending alert. Running the LLM on a fingerprint with no
	// alert payload makes the model hallucinate (it grabs random tools and
	// fishes for context — see "loki-read OOMKill in a rabbitmq thread"
	// reports). Fail fast with a clear in-thread message instead.
	if isSyntheticMention(alert) {
		p.logger.Info("synthetic mention without pending alert — sending hint, skipping LLM",
			"fingerprint", alert.Fingerprint,
			"messenger", func() string {
				if alert.ReplyTarget != nil {
					return alert.ReplyTarget.Messenger
				}
				return ""
			}(),
		)
		hint := &domain.AnalysisResult{
			AlertFingerprint: alert.Fingerprint,
			Text: "I could not find a stored alert for this thread. " +
				"Please mention me as a **reply to the original alert message**, " +
				"not to one of my previous answers.",
		}
		return p.send(ctx, alert, hint)
	}

	// Synthetic mention alerts (e.g. "thread-mention" produced by Slack/Teams
	// listeners when @bot is pinged in a thread without a corresponding
	// pending entry) must NOT touch the cache: their fingerprint is computed
	// from a near-empty label set, which collides across unrelated threads
	// and pollutes the cache with junk "I do not see alert data" responses.
	skipCache := isSyntheticMention(alert)

	// Check cache (unless reanalyze is requested or this is a synthetic mention).
	if !skipCache && alert.UserCommand != "reanalyze" {
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
	if !skipCache {
		metrics.CacheMisses.Inc()
	}

	// Analyze via LLM.
	start := time.Now()
	result, err := p.analyzer.Analyze(ctx, alert)
	if err != nil {
		metrics.AlertsAnalyzed.WithLabelValues("error").Inc()
		p.sendError(ctx, alert, err)
		return fmt.Errorf("pipeline: analyze: %w", err)
	}
	duration := time.Since(start).Seconds()

	recordAnalysisMetrics(alert, result, duration)

	// Store in cache (skip synthetic mentions to avoid cross-thread pollution).
	if !skipCache {
		if err := p.cache.Set(ctx, result); err != nil {
			p.logger.Warn("failed to cache result", "error", err)
		}
	}

	return p.send(ctx, alert, result)
}

// isSyntheticMention reports whether the alert was synthesized by a messenger
// listener from a bare @bot mention with no corresponding pending alert. These
// alerts are user-driven, ephemeral, and have no stable fingerprint — we must
// not cache them.
func isSyntheticMention(alert *domain.Alert) bool {
	if alert == nil {
		return false
	}
	switch alert.Name {
	case "thread-mention", "teams-mention":
		return true
	}
	return false
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

	// Persist the alert under each posted message ref so that a later
	// "@bot analyze" reply in the thread can recover the original alert via
	// PendingStore even when the pipeline runs in auto mode. Without this,
	// mentions on top of auto-mode alerts hit a synthetic "thread-mention"
	// alert and produce empty analyses.
	if p.pending != nil {
		for _, ref := range refs {
			if ref == nil || ref.MessageID == "" {
				continue
			}
			if err := p.pending.SavePending(ctx, ref, alert); err != nil {
				p.logger.Warn("phase 1: save pending failed",
					"messenger", ref.Messenger,
					"channel", ref.Channel,
					"message_id", ref.MessageID,
					"error", err,
				)
			}
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

	recordAnalysisMetrics(alert, result, duration)

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

// recordAnalysisMetrics records Prometheus metrics after a successful analysis.
func recordAnalysisMetrics(alert *domain.Alert, result *domain.AnalysisResult, duration float64) {
	metrics.AlertsAnalyzed.WithLabelValues("success").Inc()
	metrics.TokensTotal.WithLabelValues("input").Add(float64(result.InputTokens))
	metrics.TokensTotal.WithLabelValues("output").Add(float64(result.OutputTokens))
	metrics.CostTotal.Add(pricing.EstimateCost(result.Model, result.InputTokens, result.OutputTokens, result.InputTokenCost, result.OutputTokenCost))
	metrics.AnalysisDuration.Observe(duration)
	metrics.AnalysisDurationBySource.WithLabelValues(alert.Source).Observe(duration)
	metrics.AnalysisIterations.Observe(float64(result.Iterations))
}
