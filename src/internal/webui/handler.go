package webui

import (
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// Handler serves the web UI dashboard.
type Handler struct {
	cache  domain.Cache
	logger *slog.Logger
	tmpl   *template.Template
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

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"alerts": results,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
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

	writeJSON(w, http.StatusOK, result)
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
