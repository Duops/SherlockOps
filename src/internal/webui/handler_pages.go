package webui

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

// HealthSource exposes the latest tool connectivity results.
type HealthSource interface {
	Snapshot() []domain.ToolHealth
}

// ReviewSource reads and triggers LLM noise-reduction reviews.
type ReviewSource interface {
	LatestReview(ctx context.Context, env string) (*domain.AlertReview, error)
	StartReview(env string, window time.Duration) error
	Running() bool
	LastError() string
}

// SetReviewSource wires the alert review generator for the Stats page.
func (h *Handler) SetReviewSource(r ReviewSource) {
	h.review = r
}

type pageData struct {
	Active string
}

// SetHealthSource wires the tool health monitor for the Tools page.
func (h *Handler) SetHealthSource(s HealthSource) {
	h.health = s
}

// SetStatsProvider wires the alert volume statistics for the Stats page.
func (h *Handler) SetStatsProvider(s domain.StatsProvider) {
	h.stats = s
}

func (h *Handler) renderPage(w http.ResponseWriter, name, active string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, pageData{Active: active}); err != nil {
		h.logger.Error("render page", "page", name, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (h *Handler) statsPage(w http.ResponseWriter, _ *http.Request) {
	h.renderPage(w, "stats.html", "stats")
}

func (h *Handler) healthPage(w http.ResponseWriter, _ *http.Request) {
	h.renderPage(w, "health.html", "health")
}

type toolHealthSummary struct {
	Total        int `json:"total"`
	OK           int `json:"ok"`
	Failed       int `json:"failed"`
	Unchecked    int `json:"unchecked"`
	Environments int `json:"environments"`
}

func (h *Handler) apiToolHealth(w http.ResponseWriter, _ *http.Request) {
	tools := []domain.ToolHealth{}
	if h.health != nil {
		tools = h.health.Snapshot()
	}
	var sum toolHealthSummary
	envs := map[string]struct{}{}
	for _, t := range tools {
		sum.Total++
		envs[t.Environment] = struct{}{}
		switch t.Status {
		case domain.ToolHealthOK:
			sum.OK++
		case domain.ToolHealthFailed:
			sum.Failed++
		default:
			sum.Unchecked++
		}
	}
	sum.Environments = len(envs)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tools":   tools,
		"summary": sum,
	})
}

func (h *Handler) apiAlertStats(w http.ResponseWriter, r *http.Request) {
	if h.stats == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "stats unavailable"})
		return
	}
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 365 {
			days = n
		}
	}
	env := r.URL.Query().Get("env")
	if len(env) > 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "env too long"})
		return
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	stats, err := h.stats.AlertStats(r.Context(), since, env)
	if err != nil {
		h.logger.Error("alert stats", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to compute stats"})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func parseDays(r *http.Request, def int) int {
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 365 {
			return n
		}
	}
	return def
}

func (h *Handler) apiAlertReview(w http.ResponseWriter, r *http.Request) {
	if h.review == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "review unavailable"})
		return
	}
	env := r.URL.Query().Get("env")
	if len(env) > 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "env too long"})
		return
	}
	rev, err := h.review.LatestReview(r.Context(), env)
	if err != nil {
		h.logger.Error("latest review", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load review"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"review":     rev,
		"running":    h.review.Running(),
		"last_error": h.review.LastError(),
	})
}

func (h *Handler) apiAlertReviewRun(w http.ResponseWriter, r *http.Request) {
	if h.review == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "review unavailable"})
		return
	}
	env := r.URL.Query().Get("env")
	if len(env) > 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "env too long"})
		return
	}
	window := time.Duration(parseDays(r, 7)) * 24 * time.Hour
	if err := h.review.StartReview(env, window); err != nil {
		if errors.Is(err, errReviewRunning) || h.review.Running() {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "review already running"})
			return
		}
		h.logger.Error("start review", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to start review"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"running": true})
}

// errReviewRunning mirrors review.ErrAlreadyRunning without importing the package.
var errReviewRunning = errors.New("review already running")
