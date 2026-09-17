package webui

import (
	"context"
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
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
	health  HealthSource
	stats   domain.StatsProvider
	review  ReviewSource
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
	mux.HandleFunc("GET /ui/stats", h.statsPage)
	mux.HandleFunc("GET /ui/health", h.healthPage)
	mux.HandleFunc("GET /ui/api/health/tools", h.apiToolHealth)
	mux.HandleFunc("GET /ui/api/alert-stats", h.apiAlertStats)
	mux.HandleFunc("GET /ui/api/alert-review", h.apiAlertReview)
	mux.HandleFunc("POST /ui/api/alert-review/run", h.apiAlertReviewRun)
	mux.HandleFunc("GET /ui/api/alerts", h.apiAlerts)
	mux.HandleFunc("GET /ui/api/alerts/{fingerprint}", h.apiAlert)
	mux.HandleFunc("GET /ui/api/stats", h.apiStats)
	sub, _ := fs.Sub(content, "static")
	mux.Handle("GET /ui/static/", http.StripPrefix("/ui/static/", http.FileServerFS(sub)))
}

func (h *Handler) dashboard(w http.ResponseWriter, _ *http.Request) {
	h.renderPage(w, "dashboard.html", "alerts")
}

func (h *Handler) apiAlerts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	limit := 50
	offset := 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	filter := domain.AlertFilter{
		Source:      clip(q.Get("source")),
		Environment: clip(q.Get("env")),
		Severity:    clip(q.Get("severity")),
		Status:      clip(q.Get("status")),
		Search:      clip(q.Get("q")),
	}

	var (
		results []*domain.AnalysisResult
		total   int
		err     error
		facets  *domain.AlertFacets
	)
	if fl, ok := h.cache.(domain.FilteredLister); ok {
		results, total, err = fl.ListFiltered(ctx, filter, limit, offset)
		if err == nil {
			if facets, err = fl.Facets(ctx); err != nil {
				h.logger.Warn("alert facets", "error", err)
				facets, err = nil, nil
			}
		}
	} else {
		results, total, err = h.cache.List(ctx, limit, offset)
		if err == nil {
			results = filterResults(results, filter)
		}
	}
	if err != nil {
		h.logger.Error("list alerts", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list alerts"})
		return
	}

	// Manual-mode pending alerts are merged into the first page only, deduped
	// by fingerprint and subject to the same filters.
	merged := results
	pendingCount := 0
	if h.pending != nil && offset == 0 {
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
				if !matchesFilter(stub, filter) {
					continue
				}
				merged = append(merged, stub)
				pendingCount++
			}
		}
	}

	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].CachedAt.After(merged[j].CachedAt)
	})

	resp := map[string]interface{}{
		"alerts":  toAPIAlerts(merged),
		"total":   total + pendingCount,
		"pending": pendingCount,
		"limit":   limit,
		"offset":  offset,
	}
	if facets != nil {
		resp["facets"] = facets
	}
	writeJSON(w, http.StatusOK, resp)
}

func clip(v string) string {
	if len(v) > 128 {
		return v[:128]
	}
	return strings.TrimSpace(v)
}

// matchesFilter applies an AlertFilter in Go (for pending stubs and caches
// without server-side filtering).
func matchesFilter(r *domain.AnalysisResult, f domain.AlertFilter) bool {
	if r == nil {
		return false
	}
	env := r.Environment
	if env == "" {
		env = "default"
	}
	if f.Source != "" && r.Source != f.Source {
		return false
	}
	if f.Environment != "" && env != f.Environment {
		return false
	}
	if f.Severity != "" && r.Severity != f.Severity {
		return false
	}
	resolved := r.ResolvedAt != nil
	if f.Status == "resolved" && !resolved || f.Status == "firing" && resolved {
		return false
	}
	if f.Search != "" {
		q := strings.ToLower(f.Search)
		hay := strings.ToLower(r.AlertName + " " + r.AlertFingerprint + " " + r.Text)
		if !strings.Contains(hay, q) {
			return false
		}
	}
	return true
}

func filterResults(rs []*domain.AnalysisResult, f domain.AlertFilter) []*domain.AnalysisResult {
	if f == (domain.AlertFilter{}) {
		return rs
	}
	out := make([]*domain.AnalysisResult, 0, len(rs))
	for _, r := range rs {
		if matchesFilter(r, f) {
			out = append(out, r)
		}
	}
	return out
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
		Environment:      a.Environment,
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
