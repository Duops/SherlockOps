package health

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

type stubCache struct {
	getErr error
}

func (s *stubCache) Get(_ context.Context, _ string) (*domain.AnalysisResult, error) {
	return nil, s.getErr
}
func (s *stubCache) Set(_ context.Context, _ *domain.AnalysisResult) error { return nil }
func (s *stubCache) MarkResolved(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (s *stubCache) Close() error { return nil }
func (s *stubCache) List(_ context.Context, _ int, _ int) ([]*domain.AnalysisResult, int, error) {
	return nil, 0, nil
}
func (s *stubCache) Stats(_ context.Context) (*domain.CacheStats, error) {
	return &domain.CacheStats{}, nil
}

type stubMessenger struct {
	name string
}

func (s *stubMessenger) Name() string { return s.name }
func (s *stubMessenger) Start(_ context.Context, _ func(*domain.Alert)) error {
	return nil
}
func (s *stubMessenger) SendAlert(_ context.Context, _ *domain.Alert) (*domain.MessageRef, error) {
	return nil, nil
}
func (s *stubMessenger) SendAnalysisReply(_ context.Context, _ *domain.MessageRef, _ *domain.AnalysisResult) error {
	return nil
}
func (s *stubMessenger) SendAnalysis(_ context.Context, _ *domain.Alert, _ *domain.AnalysisResult) error {
	return nil
}
func (s *stubMessenger) SendError(_ context.Context, _ *domain.Alert, _ error) error { return nil }
func (s *stubMessenger) Stop(_ context.Context) error                                { return nil }

func TestLiveness_ReturnsOK(t *testing.T) {
	checker := NewChecker(&stubCache{}, nil, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rec := httptest.NewRecorder()
	checker.Liveness(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status ok, got %s", resp.Status)
	}
}

func TestReadiness_HealthyCache(t *testing.T) {
	checker := NewChecker(
		&stubCache{},
		[]domain.Messenger{&stubMessenger{name: "slack"}},
		slog.Default(),
	)

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	checker.Readiness(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status ok, got %s", resp.Status)
	}
	if resp.Checks["cache"] != "ok" {
		t.Errorf("expected cache check ok, got %s", resp.Checks["cache"])
	}
	if resp.Checks["messenger_slack"] != "configured" {
		t.Errorf("expected messenger_slack configured, got %s", resp.Checks["messenger_slack"])
	}
}

func TestReadiness_UnhealthyCache(t *testing.T) {
	checker := NewChecker(
		&stubCache{getErr: errors.New("connection refused")},
		nil,
		slog.Default(),
	)

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	checker.Readiness(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}

	var resp healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != "degraded" {
		t.Errorf("expected status degraded, got %s", resp.Status)
	}
}

func TestReadiness_NoMessengers(t *testing.T) {
	checker := NewChecker(&stubCache{}, nil, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	checker.Readiness(rec, req)

	var resp healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Checks["messengers"] != "none configured" {
		t.Errorf("expected 'none configured', got %s", resp.Checks["messengers"])
	}
}
