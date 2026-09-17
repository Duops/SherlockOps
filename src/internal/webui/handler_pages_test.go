package webui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

type stubHealth struct{ tools []domain.ToolHealth }

func (s *stubHealth) Snapshot() []domain.ToolHealth { return s.tools }

type stubStats struct {
	gotSince time.Time
	gotEnv   string
	err      error
}

func (s *stubStats) AlertStats(_ context.Context, since time.Time, env string) (*domain.AlertStats, error) {
	s.gotSince, s.gotEnv = since, env
	if s.err != nil {
		return nil, s.err
	}
	return &domain.AlertStats{Total: 42, Environment: env, Environments: []string{"prod"}}, nil
}

func newPagesHandler() (*Handler, *http.ServeMux) {
	h := New(&mockCache{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return h, mux
}

func get(mux *http.ServeMux, url string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	return rec
}

func TestPages_RenderWithActiveNav(t *testing.T) {
	_, mux := newPagesHandler()
	cases := map[string]string{
		"/ui":        `href="/ui" class="active"`,
		"/ui/stats":  `href="/ui/stats" class="active"`,
		"/ui/health": `href="/ui/health" class="active"`,
	}
	for url, want := range cases {
		rec := get(mux, url)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", url, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: expected %q in body", url, want)
		}
	}
}

func TestAPIToolHealth_SummarizesSnapshot(t *testing.T) {
	h, mux := newPagesHandler()
	h.SetHealthSource(&stubHealth{tools: []domain.ToolHealth{
		{Environment: "default", Tool: "kubernetes", Status: domain.ToolHealthOK},
		{Environment: "prod", Tool: "loki", Status: domain.ToolHealthFailed, Error: "HTTP 400"},
		{Environment: "prod", Tool: "vsphere", Status: domain.ToolHealthUnchecked},
	}})

	rec := get(mux, "/ui/api/health/tools")
	var body struct {
		Tools   []domain.ToolHealth `json:"tools"`
		Summary toolHealthSummary   `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Tools) != 3 {
		t.Errorf("tools = %d, want 3", len(body.Tools))
	}
	want := toolHealthSummary{Total: 3, OK: 1, Failed: 1, Unchecked: 1, Environments: 2}
	if body.Summary != want {
		t.Errorf("summary = %+v, want %+v", body.Summary, want)
	}
}

func TestAPIToolHealth_WithoutSourceReturnsEmpty(t *testing.T) {
	_, mux := newPagesHandler()
	rec := get(mux, "/ui/api/health/tools")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tools":[]`) {
		t.Errorf("unexpected response %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIAlertStats_PassesWindowAndEnv(t *testing.T) {
	h, mux := newPagesHandler()
	stats := &stubStats{}
	h.SetStatsProvider(stats)

	rec := get(mux, "/ui/api/alert-stats?days=7&env=prod")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if stats.gotEnv != "prod" {
		t.Errorf("env = %q, want prod", stats.gotEnv)
	}
	age := time.Since(stats.gotSince)
	if age < 7*24*time.Hour-time.Minute || age > 7*24*time.Hour+time.Minute {
		t.Errorf("since is %v ago, want ~7 days", age)
	}
	if !strings.Contains(rec.Body.String(), `"total":42`) {
		t.Errorf("expected stats payload, got %s", rec.Body.String())
	}
}

func TestAPIAlertStats_DefaultsAndErrors(t *testing.T) {
	h, mux := newPagesHandler()
	if rec := get(mux, "/ui/api/alert-stats"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without provider: status %d, want 503", rec.Code)
	}

	stats := &stubStats{}
	h.SetStatsProvider(stats)
	get(mux, "/ui/api/alert-stats?days=9999")
	if age := time.Since(stats.gotSince); age < 30*24*time.Hour-time.Minute || age > 30*24*time.Hour+time.Minute {
		t.Errorf("invalid days should fall back to 30, since is %v ago", age)
	}

	stats.err = errors.New("boom")
	if rec := get(mux, "/ui/api/alert-stats"); rec.Code != http.StatusInternalServerError {
		t.Errorf("provider error: status %d, want 500", rec.Code)
	}
}

type stubReview struct {
	latest  *domain.AlertReview
	running bool
	started []string
	err     error
}

func (s *stubReview) LatestReview(_ context.Context, env string) (*domain.AlertReview, error) {
	if s.latest != nil && s.latest.Environment == env {
		return s.latest, nil
	}
	return nil, nil
}
func (s *stubReview) StartReview(env string, window time.Duration) error {
	s.started = append(s.started, env+"/"+window.String())
	return s.err
}
func (s *stubReview) Running() bool     { return s.running }
func (s *stubReview) LastError() string { return "" }

func TestAPIAlertReview(t *testing.T) {
	h, mux := newPagesHandler()
	if rec := get(mux, "/ui/api/alert-review"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without source: %d", rec.Code)
	}
	stub := &stubReview{latest: &domain.AlertReview{Environment: "prod", Text: "cut TargetDown"}}
	h.SetReviewSource(stub)

	rec := get(mux, "/ui/api/alert-review")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"review":null`) {
		t.Errorf("all envs should have no review: %d %s", rec.Code, rec.Body.String())
	}
	rec = get(mux, "/ui/api/alert-review?env=prod")
	if !strings.Contains(rec.Body.String(), "cut TargetDown") {
		t.Errorf("prod review missing: %s", rec.Body.String())
	}
}

func TestAPIAlertReviewRun(t *testing.T) {
	h, mux := newPagesHandler()
	stub := &stubReview{}
	h.SetReviewSource(stub)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ui/api/alert-review/run?env=prod&days=14", nil))
	if rec.Code != http.StatusAccepted || len(stub.started) != 1 || stub.started[0] != "prod/336h0m0s" {
		t.Errorf("run: %d, started=%v", rec.Code, stub.started)
	}

	stub.err = errReviewRunning
	stub.running = true
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ui/api/alert-review/run", nil))
	if rec.Code != http.StatusConflict {
		t.Errorf("concurrent run: %d, want 409", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/api/alert-review/run", nil))
	if rec.Code == http.StatusAccepted {
		t.Error("GET must not start a review")
	}
}
