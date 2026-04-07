package webui

import (
	"context"
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
	"github.com/Duops/SherlockOps/internal/pricing"
)

// PendingLister surfaces manual-mode alerts that have been received but not
// yet analyzed, so the dashboard can show them alongside analyzed entries.
type PendingLister interface {
	ListPending(ctx context.Context, limit int) ([]PendingItem, error)
}

// PendingItem is a minimal projection of a pending alert for the dashboard.
type PendingItem struct {
	Alert     *domain.Alert
	CreatedAt time.Time
}

// Handler serves the web UI dashboard.
type Handler struct {
	cache   domain.Cache
	pending PendingLister
	logger  *slog.Logger
	tmpl    *template.Template
}

// New creates a Handler with the given cache and logger.
func New(cache domain.Cache, logger *slog.Logger) *Handler {
	tmpl := template.Must(template.ParseFS(content, "templates/*.html"))
	return &Handler{
		cache:  cache,
		logger: logger,
		tmpl:   tmpl,
	}
}

// SetPendingLister wires the source of unanalyzed manual-mode alerts.
func (h *Handler) SetPendingLister(p PendingLister) {
	h.pending = p
}

// RegisterRoutes adds the dashboard routes to the given ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui", h.dashboard)
	mux.HandleFunc("GET /ui/api/alerts", h.apiAlerts)
	mux.HandleFunc("GET /ui/api/alerts/{fingerprint}", h.apiAlert)
	mux.HandleFunc("GET /ui/api/stats", h.apiStats)
	sub, _ := fs.Sub(content, "static")
	mux.Handle("GET /ui/static/", http.StripPrefix("/ui/static/", http.FileServerFS(sub)))
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "dashboard.html", nil); err != nil {
		h.logger.Error("render dashboard", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (h *Handler) apiAlerts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	results, total, err := h.cache.List(ctx, limit, offset)
	if err != nil {
		h.logger.Error("list alerts", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list alerts"})
		return
	}

	// Merge in manual-mode pending alerts (received but not yet analyzed),
	// deduped by fingerprint against the analyzed cache.
	merged := results
	pendingCount := 0
	if h.pending != nil {
		pendingItems, perr := h.pending.ListPending(ctx, limit)
		if perr != nil {
			h.logger.Warn("list pending alerts", "error", perr)
		} else {
			seen := make(map[string]struct{}, len(results))
			for _, r := range results {
				if r != nil {
					seen[r.AlertFingerprint] = struct{}{}
				}
			}
			for _, it := range pendingItems {
				if it.Alert == nil {
					continue
				}
				if _, dup := seen[it.Alert.Fingerprint]; dup {
					continue
				}
				stub := pendingToStub(it)
				merged = append(merged, stub)
				pendingCount++
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"alerts":  toAPIAlerts(merged),
		"total":   total + pendingCount,
		"pending": pendingCount,
		"limit":   limit,
		"offset":  offset,
	})
}

// apiAlert is the wire shape returned by /ui/api/alerts. It embeds the cached
// AnalysisResult and adds derived fields the dashboard needs (estimated USD
// cost, total tokens) so the JS does not have to know the pricing table.
type apiAlert struct {
	*domain.AnalysisResult
	CostUSD float64 `json:"cost_usd"`
}

func toAPIAlert(r *domain.AnalysisResult) apiAlert {
	if r == nil {
		return apiAlert{}
	}
	return apiAlert{
		AnalysisResult: r,
		CostUSD:        pricing.EstimateCost(r.Model, r.InputTokens, r.OutputTokens, r.InputTokenCost, r.OutputTokenCost),
	}
}

func toAPIAlerts(rs []*domain.AnalysisResult) []apiAlert {
	out := make([]apiAlert, 0, len(rs))
	for _, r := range rs {
		out = append(out, toAPIAlert(r))
	}
	return out
}

// pendingToStub converts a pending entry into an AnalysisResult-shaped object
// so the dashboard can render it in the same table. Text holds the raw alert
// payload as a placeholder until analysis is requested via @bot mention.
func pendingToStub(it PendingItem) *domain.AnalysisResult {
	a := it.Alert
	text := a.RawText
	if text == "" {
		text = "(awaiting @bot analyze — alert received in manual mode)"
	}
	return &domain.AnalysisResult{
		AlertFingerprint: a.Fingerprint,
		AlertName:        a.Name,
		Source:           a.Source,
		Severity:         string(a.Severity),
		Text:             text,
		ToolsUsed:        nil,
		CachedAt:         it.CreatedAt,
	}
}

func (h *Handler) apiAlert(w http.ResponseWriter, r *http.Request) {
	fingerprint := r.PathValue("fingerprint")
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fingerprint required"})
		return
	}

	result, err := h.cache.Get(r.Context(), fingerprint)
	if err != nil {
		h.logger.Error("get alert", "error", err, "fingerprint", fingerprint)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get alert"})
		return
	}
	if result == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	writeJSON(w, http.StatusOK, toAPIAlert(result))
}

func (h *Handler) apiStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.cache.Stats(r.Context())
	if err != nil {
		h.logger.Error("get stats", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get stats"})
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
