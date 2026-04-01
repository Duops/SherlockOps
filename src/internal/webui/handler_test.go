package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// mockCache implements domain.Cache for testing.
type mockCache struct {
	alerts []*domain.AnalysisResult
}

func (m *mockCache) Get(_ context.Context, fingerprint string) (*domain.AnalysisResult, error) {
	for _, a := range m.alerts {
		if a.AlertFingerprint == fingerprint {
			return a, nil
		}
	}
	return nil, nil
}

func (m *mockCache) Set(_ context.Context, _ *domain.AnalysisResult) error { return nil }
func (m *mockCache) MarkResolved(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (m *mockCache) Close() error { return nil }

func (m *mockCache) List(_ context.Context, limit int, offset int) ([]*domain.AnalysisResult, int, error) {
	total := len(m.alerts)
	if offset >= total {
		return nil, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return m.alerts[offset:end], total, nil
}

func (m *mockCache) Stats(_ context.Context) (*domain.CacheStats, error) {
	resolved := 0
	totalLen := 0
	for _, a := range m.alerts {
		if a.ResolvedAt != nil {
			resolved++
		}
		totalLen += len(a.Text)
	}
	avg := 0.0
	if len(m.alerts) > 0 {
		avg = float64(totalLen) / float64(len(m.alerts))
	}
	return &domain.CacheStats{
		TotalCount:    len(m.alerts),
		ResolvedCount: resolved,
		AvgTextLength: avg,
	}, nil
}

// errCache implements domain.Cache and always returns errors.
type errCache struct{}

func (e *errCache) Get(_ context.Context, _ string) (*domain.AnalysisResult, error) {
	return nil, errFake
}
func (e *errCache) Set(_ context.Context, _ *domain.AnalysisResult) error { return errFake }
func (e *errCache) MarkResolved(_ context.Context, _ string, _ time.Time) error {
	return errFake
}
func (e *errCache) Close() error { return nil }
func (e *errCache) List(_ context.Context, _ int, _ int) ([]*domain.AnalysisResult, int, error) {
	return nil, 0, errFake
}
func (e *errCache) Stats(_ context.Context) (*domain.CacheStats, error) {
	return nil, errFake
}

var errFake = fmt.Errorf("fake cache error")

func newErrHandler() (*Handler, *http.ServeMux) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New(&errCache{}, logger)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return h, mux
}

func newTestHandler() (*Handler, *http.ServeMux) {
	now := time.Now()
	resolved := now.Add(-time.Hour)
	cache := &mockCache{
		alerts: []*domain.AnalysisResult{
			{
				AlertFingerprint: "abc123",
				Text:             "High CPU usage detected on prod-web-01",
				ToolsUsed:        []string{"kubectl", "prometheus"},
				CachedAt:         now,
			},
			{
				AlertFingerprint: "def456",
				Text:             "Disk space warning on db-replica-02",
				ToolsUsed:        []string{"ssh"},
				CachedAt:         now.Add(-30 * time.Minute),
				ResolvedAt:       &resolved,
			},
		},
	}

	logger := slog.Default()
	h := New(cache, logger)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return h, mux
}

func TestDashboardReturnsHTML(t *testing.T) {
	_, mux := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Fatalf("expected text/html content type, got %q", ct)
	}

	body := rec.Body.String()
	if len(body) == 0 {
		t.Fatal("expected non-empty HTML body")
	}
	if !containsStr(body, "SherlockOps") {
		t.Fatal("expected HTML to contain 'SherlockOps'")
	}
}

func TestAPIAlertsReturnsJSON(t *testing.T) {
	_, mux := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/ui/api/alerts", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	if _, ok := result["alerts"]; !ok {
		t.Fatal("expected 'alerts' key in response")
	}
	if _, ok := result["total"]; !ok {
		t.Fatal("expected 'total' key in response")
	}

	var alerts []domain.AnalysisResult
	if err := json.Unmarshal(result["alerts"], &alerts); err != nil {
		t.Fatalf("failed to decode alerts: %v", err)
	}
	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(alerts))
	}
}

func TestAPIStatsReturnsJSON(t *testing.T) {
	_, mux := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/ui/api/stats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var stats domain.CacheStats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("failed to decode stats: %v", err)
	}

	if stats.TotalCount != 2 {
		t.Fatalf("expected total_count=2, got %d", stats.TotalCount)
	}
	if stats.ResolvedCount != 1 {
		t.Fatalf("expected resolved_count=1, got %d", stats.ResolvedCount)
	}
}

func TestAPIAlertByFingerprint(t *testing.T) {
	_, mux := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/ui/api/alerts/abc123", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result domain.AnalysisResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if result.AlertFingerprint != "abc123" {
		t.Fatalf("expected fingerprint abc123, got %q", result.AlertFingerprint)
	}
}

func TestAPIAlertNotFound(t *testing.T) {
	_, mux := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/ui/api/alerts/nonexistent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ---------- apiAlerts: custom limit/offset ----------

func TestAPIAlertsWithLimitAndOffset(t *testing.T) {
	_, mux := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/ui/api/alerts?limit=1&offset=0", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	var alerts []domain.AnalysisResult
	if err := json.Unmarshal(result["alerts"], &alerts); err != nil {
		t.Fatalf("failed to decode alerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert with limit=1, got %d", len(alerts))
	}
}

func TestAPIAlertsWithOffset(t *testing.T) {
	_, mux := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/ui/api/alerts?limit=10&offset=1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	var alerts []domain.AnalysisResult
	if err := json.Unmarshal(result["alerts"], &alerts); err != nil {
		t.Fatalf("failed to decode alerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert with offset=1, got %d", len(alerts))
	}
}

func TestAPIAlertsInvalidParams(t *testing.T) {
	_, mux := newTestHandler()

	// Invalid limit (not a number) and negative offset should be ignored, defaults used.
	req := httptest.NewRequest(http.MethodGet, "/ui/api/alerts?limit=abc&offset=-5", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	var alerts []domain.AnalysisResult
	if err := json.Unmarshal(result["alerts"], &alerts); err != nil {
		t.Fatalf("failed to decode alerts: %v", err)
	}
	// defaults: limit=50, offset=0 => all 2 alerts
	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts with default params, got %d", len(alerts))
	}
}

func TestAPIAlertsLimitZeroIgnored(t *testing.T) {
	_, mux := newTestHandler()

	// limit=0 is not > 0, so default 50 should be used
	req := httptest.NewRequest(http.MethodGet, "/ui/api/alerts?limit=0", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]json.RawMessage
	json.Unmarshal(rec.Body.Bytes(), &result)
	var alerts []domain.AnalysisResult
	json.Unmarshal(result["alerts"], &alerts)
	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(alerts))
	}
}

func TestAPIAlertsLimitOver200Ignored(t *testing.T) {
	_, mux := newTestHandler()

	// limit=300 is > 200, so default 50 should be used
	req := httptest.NewRequest(http.MethodGet, "/ui/api/alerts?limit=300", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------- Error paths ----------

func TestAPIAlertsError(t *testing.T) {
	_, mux := newErrHandler()

	req := httptest.NewRequest(http.MethodGet, "/ui/api/alerts", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if body["error"] != "failed to list alerts" {
		t.Fatalf("unexpected error message: %q", body["error"])
	}
}

func TestAPIAlertError(t *testing.T) {
	_, mux := newErrHandler()

	req := httptest.NewRequest(http.MethodGet, "/ui/api/alerts/abc123", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if body["error"] != "failed to get alert" {
		t.Fatalf("unexpected error message: %q", body["error"])
	}
}

func TestAPIStatsError(t *testing.T) {
	_, mux := newErrHandler()

	req := httptest.NewRequest(http.MethodGet, "/ui/api/stats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if body["error"] != "failed to get stats" {
		t.Fatalf("unexpected error message: %q", body["error"])
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
