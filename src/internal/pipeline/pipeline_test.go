package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

// --- mocks ---

type mockCache struct {
	store    map[string]*domain.AnalysisResult
	resolved map[string]time.Time
	getErr   error
	setErr   error
}

func newMockCache() *mockCache {
	return &mockCache{
		store:    make(map[string]*domain.AnalysisResult),
		resolved: make(map[string]time.Time),
	}
}

func (m *mockCache) Get(_ context.Context, fp string) (*domain.AnalysisResult, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.store[fp], nil
}

func (m *mockCache) Set(_ context.Context, r *domain.AnalysisResult) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.store[r.AlertFingerprint] = r
	return nil
}

func (m *mockCache) MarkResolved(_ context.Context, fp string, t time.Time) error {
	m.resolved[fp] = t
	return nil
}

func (m *mockCache) Close() error { return nil }

func (m *mockCache) List(_ context.Context, limit int, offset int) ([]*domain.AnalysisResult, int, error) {
	all := make([]*domain.AnalysisResult, 0, len(m.store))
	for _, v := range m.store {
		all = append(all, v)
	}
	total := len(all)
	if offset >= total {
		return nil, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total, nil
}

func (m *mockCache) Stats(_ context.Context) (*domain.CacheStats, error) {
	return &domain.CacheStats{TotalCount: len(m.store)}, nil
}

type mockAnalyzer struct {
	result *domain.AnalysisResult
	err    error
	called int
}

func (m *mockAnalyzer) Analyze(_ context.Context, a *domain.Alert) (*domain.AnalysisResult, error) {
	m.called++
	if m.err != nil {
		return nil, m.err
	}
	r := *m.result
	r.AlertFingerprint = a.Fingerprint
	return &r, nil
}

type mockMessenger struct {
	name           string
	sent           []*domain.AnalysisResult
	alertsSent     []*domain.Alert
	analysisReplies []*domain.AnalysisResult
	errors         []error
	sendErr        error
	sendErrErr     error
	sendAlertErr   error
}

func (m *mockMessenger) Name() string { return m.name }

func (m *mockMessenger) Start(_ context.Context, _ func(*domain.Alert)) error { return nil }

func (m *mockMessenger) SendAlert(_ context.Context, alert *domain.Alert) (*domain.MessageRef, error) {
	if m.sendAlertErr != nil {
		return nil, m.sendAlertErr
	}
	m.alertsSent = append(m.alertsSent, alert)
	return &domain.MessageRef{
		Messenger: m.name,
		Channel:   "test-channel",
		MessageID: "test-msg-id",
		Alert:     alert,
	}, nil
}

func (m *mockMessenger) SendAnalysisReply(_ context.Context, _ *domain.MessageRef, r *domain.AnalysisResult) error {
	m.analysisReplies = append(m.analysisReplies, r)
	return m.sendErr
}

func (m *mockMessenger) SendAnalysis(_ context.Context, _ *domain.Alert, r *domain.AnalysisResult) error {
	m.sent = append(m.sent, r)
	return m.sendErr
}

func (m *mockMessenger) SendError(_ context.Context, _ *domain.Alert, err error) error {
	m.errors = append(m.errors, err)
	return m.sendErrErr
}

func (m *mockMessenger) Stop(_ context.Context) error { return nil }

// --- helpers ---

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// baseAlert returns an alert with a ReplyTarget but no ThreadID, triggering two-phase flow.
func baseAlert() *domain.Alert {
	return &domain.Alert{
		Name:        "HighCPU",
		Status:      domain.StatusFiring,
		Labels:      map[string]string{"namespace": "prod"},
		Fingerprint: "test-fp-001",
		ReplyTarget: &domain.ReplyTarget{Messenger: "slack", Channel: "C123"},
	}
}

// baseBotAlert returns an alert from bot listener mode (has ThreadID), triggering single-phase flow.
func baseBotAlert() *domain.Alert {
	return &domain.Alert{
		Name:        "HighCPU",
		Status:      domain.StatusFiring,
		Labels:      map[string]string{"namespace": "prod"},
		Fingerprint: "test-fp-001",
		ReplyTarget: &domain.ReplyTarget{Messenger: "slack", Channel: "C123", ThreadID: "1234.5678"},
	}
}

// --- tests ---

// --- Two-phase flow tests (webhook-originated alerts, no ThreadID) ---

func TestProcess_TwoPhase_CacheMiss(t *testing.T) {
	cache := newMockCache()
	analyzer := &mockAnalyzer{result: &domain.AnalysisResult{Text: "analysis result"}}
	messenger := &mockMessenger{name: "slack"}

	p := New(cache, analyzer, []domain.Messenger{messenger}, testLogger())

	alert := baseAlert()
	if err := p.Process(context.Background(), alert); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if analyzer.called != 1 {
		t.Errorf("analyzer.called = %d, want 1", analyzer.called)
	}
	// Phase 1: SendAlert should be called.
	if len(messenger.alertsSent) != 1 {
		t.Fatalf("messenger.alertsSent = %d, want 1", len(messenger.alertsSent))
	}
	// Phase 2: SendAnalysisReply should be called.
	if len(messenger.analysisReplies) != 1 {
		t.Fatalf("messenger.analysisReplies = %d, want 1", len(messenger.analysisReplies))
	}
	if messenger.analysisReplies[0].Text != "analysis result" {
		t.Errorf("reply text = %q, want %q", messenger.analysisReplies[0].Text, "analysis result")
	}
	// Should be cached now.
	if _, ok := cache.store[alert.Fingerprint]; !ok {
		t.Error("result not stored in cache")
	}
}

func TestProcess_TwoPhase_CacheHit(t *testing.T) {
	cache := newMockCache()
	cached := &domain.AnalysisResult{
		AlertFingerprint: "test-fp-001",
		Text:             "cached analysis",
	}
	cache.store["test-fp-001"] = cached

	analyzer := &mockAnalyzer{result: &domain.AnalysisResult{Text: "fresh"}}
	messenger := &mockMessenger{name: "slack"}

	p := New(cache, analyzer, []domain.Messenger{messenger}, testLogger())

	if err := p.Process(context.Background(), baseAlert()); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if analyzer.called != 0 {
		t.Errorf("analyzer should not be called on cache hit, called %d times", analyzer.called)
	}
	// Phase 1: SendAlert should still be called.
	if len(messenger.alertsSent) != 1 {
		t.Errorf("expected SendAlert to be called once")
	}
	// Phase 2: SendAnalysisReply with cached result.
	if len(messenger.analysisReplies) != 1 || messenger.analysisReplies[0].Text != "cached analysis" {
		t.Errorf("expected cached analysis to be sent via reply")
	}
}

func TestProcess_TwoPhase_Resolved(t *testing.T) {
	cache := newMockCache()
	analyzer := &mockAnalyzer{result: &domain.AnalysisResult{Text: "fresh"}}
	messenger := &mockMessenger{name: "slack"}

	p := New(cache, analyzer, []domain.Messenger{messenger}, testLogger())

	alert := baseAlert()
	alert.Status = domain.StatusResolved
	alert.EndsAt = time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	if err := p.Process(context.Background(), alert); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if analyzer.called != 0 {
		t.Errorf("analyzer should not be called for resolved, called %d", analyzer.called)
	}
	// SendAlert should be called to notify about resolution.
	if len(messenger.alertsSent) != 1 {
		t.Error("should send resolved alert via SendAlert")
	}
	if _, ok := cache.resolved[alert.Fingerprint]; !ok {
		t.Error("resolved not recorded in cache")
	}
}

func TestProcess_TwoPhase_Reanalyze(t *testing.T) {
	cache := newMockCache()
	cache.store["test-fp-001"] = &domain.AnalysisResult{
		AlertFingerprint: "test-fp-001",
		Text:             "old analysis",
	}

	analyzer := &mockAnalyzer{result: &domain.AnalysisResult{Text: "fresh analysis"}}
	messenger := &mockMessenger{name: "slack"}

	p := New(cache, analyzer, []domain.Messenger{messenger}, testLogger())

	alert := baseAlert()
	alert.UserCommand = "reanalyze"

	if err := p.Process(context.Background(), alert); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if analyzer.called != 1 {
		t.Errorf("analyzer.called = %d, want 1 (reanalyze should skip cache)", analyzer.called)
	}
	if len(messenger.analysisReplies) != 1 || messenger.analysisReplies[0].Text != "fresh analysis" {
		t.Error("expected fresh analysis to be sent via reply")
	}
}

func TestProcess_TwoPhase_AnalyzerError(t *testing.T) {
	cache := newMockCache()
	analyzerErr := errors.New("llm timeout")
	analyzer := &mockAnalyzer{err: analyzerErr}
	messenger := &mockMessenger{name: "slack"}

	p := New(cache, analyzer, []domain.Messenger{messenger}, testLogger())

	err := p.Process(context.Background(), baseAlert())
	if err == nil {
		t.Fatal("expected error from analyzer")
	}

	if len(messenger.errors) != 1 {
		t.Errorf("expected SendError to be called once, got %d", len(messenger.errors))
	}
}

func TestProcess_TwoPhase_BroadcastWhenNoTarget(t *testing.T) {
	cache := newMockCache()
	analyzer := &mockAnalyzer{result: &domain.AnalysisResult{Text: "result"}}
	slack := &mockMessenger{name: "slack"}
	telegram := &mockMessenger{name: "telegram"}

	p := New(cache, analyzer, []domain.Messenger{slack, telegram}, testLogger())

	alert := baseAlert()
	alert.ReplyTarget = nil // No specific target.

	if err := p.Process(context.Background(), alert); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if len(slack.alertsSent) != 1 {
		t.Error("slack should receive SendAlert broadcast")
	}
	if len(telegram.alertsSent) != 1 {
		t.Error("telegram should receive SendAlert broadcast")
	}
	if len(slack.analysisReplies) != 1 {
		t.Error("slack should receive analysis reply")
	}
	if len(telegram.analysisReplies) != 1 {
		t.Error("telegram should receive analysis reply")
	}
}

// --- Single-phase flow tests (bot listener mode, has ThreadID) ---

func TestProcess_SinglePhase_CacheMiss(t *testing.T) {
	cache := newMockCache()
	analyzer := &mockAnalyzer{result: &domain.AnalysisResult{Text: "analysis result"}}
	messenger := &mockMessenger{name: "slack"}

	p := New(cache, analyzer, []domain.Messenger{messenger}, testLogger())

	alert := baseBotAlert()
	if err := p.Process(context.Background(), alert); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if analyzer.called != 1 {
		t.Errorf("analyzer.called = %d, want 1", analyzer.called)
	}
	// Single-phase: SendAnalysis should be called (not two-phase).
	if len(messenger.sent) != 1 {
		t.Fatalf("messenger.sent = %d, want 1", len(messenger.sent))
	}
	if messenger.sent[0].Text != "analysis result" {
		t.Errorf("sent text = %q, want %q", messenger.sent[0].Text, "analysis result")
	}
	if _, ok := cache.store[alert.Fingerprint]; !ok {
		t.Error("result not stored in cache")
	}
}

func TestProcess_SinglePhase_CacheHit(t *testing.T) {
	cache := newMockCache()
	cached := &domain.AnalysisResult{
		AlertFingerprint: "test-fp-001",
		Text:             "cached analysis",
	}
	cache.store["test-fp-001"] = cached

	analyzer := &mockAnalyzer{result: &domain.AnalysisResult{Text: "fresh"}}
	messenger := &mockMessenger{name: "slack"}

	p := New(cache, analyzer, []domain.Messenger{messenger}, testLogger())

	if err := p.Process(context.Background(), baseBotAlert()); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if analyzer.called != 0 {
		t.Errorf("analyzer should not be called on cache hit, called %d times", analyzer.called)
	}
	if len(messenger.sent) != 1 || messenger.sent[0].Text != "cached analysis" {
		t.Errorf("expected cached analysis to be sent")
	}
}

func TestProcess_SinglePhase_AnalyzerError(t *testing.T) {
	cache := newMockCache()
	analyzerErr := errors.New("llm timeout")
	analyzer := &mockAnalyzer{err: analyzerErr}
	messenger := &mockMessenger{name: "slack"}

	p := New(cache, analyzer, []domain.Messenger{messenger}, testLogger())

	err := p.Process(context.Background(), baseBotAlert())
	if err == nil {
		t.Fatal("expected error from analyzer")
	}

	if len(messenger.errors) != 1 {
		t.Errorf("expected SendError to be called once, got %d", len(messenger.errors))
	}
}

func TestProcess_ComputesFingerprint(t *testing.T) {
	cache := newMockCache()
	analyzer := &mockAnalyzer{result: &domain.AnalysisResult{Text: "result"}}
	messenger := &mockMessenger{name: "slack"}

	p := New(cache, analyzer, []domain.Messenger{messenger}, testLogger())

	alert := &domain.Alert{
		Name:   "HighMemory",
		Status: domain.StatusFiring,
		Labels: map[string]string{"namespace": "staging"},
	}

	if err := p.Process(context.Background(), alert); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if alert.Fingerprint == "" {
		t.Error("expected fingerprint to be computed")
	}
}
