package tooling

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func newTestPrometheusServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		query := r.URL.Query().Get("query")
		if query == "" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "error",
				"error":  "missing query",
			})
			return
		}

		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/query"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "vector",
					"result": []map[string]interface{}{
						{
							"metric": map[string]string{
								"__name__": "up",
								"job":      "myapp",
								"instance": "localhost:9090",
							},
							"value": []interface{}{1234567890.0, "1"},
						},
					},
				},
			})

		case strings.HasSuffix(r.URL.Path, "/api/v1/query_range"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "matrix",
					"result": []map[string]interface{}{
						{
							"metric": map[string]string{
								"__name__": "up",
								"job":      "myapp",
							},
							"values": [][]interface{}{
								{1234567890.0, "1"},
								{1234567950.0, "1"},
							},
						},
					},
				},
			})

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func TestPrometheusExecutor_ListTools(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewPrometheusExecutor("http://localhost:9090", "", "", logger)

	tools, err := exec.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if len(tools) != 5 {
		t.Fatalf("expected 5 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if !names["prometheus_query"] || !names["prometheus_query_range"] {
		t.Errorf("unexpected tool names: %v", names)
	}
}

func TestPrometheusExecutor_InstantQuery(t *testing.T) {
	srv := newTestPrometheusServer(t)
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewPrometheusExecutor(srv.URL, "", "", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-1",
		Name: "prometheus_query",
		Input: map[string]interface{}{
			"query": "up{job=\"myapp\"}",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "vector") {
		t.Errorf("expected vector in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "myapp") {
		t.Errorf("expected myapp in response, got: %s", result.Content)
	}
}

func TestPrometheusExecutor_RangeQuery(t *testing.T) {
	srv := newTestPrometheusServer(t)
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewPrometheusExecutor(srv.URL, "", "", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-2",
		Name: "prometheus_query_range",
		Input: map[string]interface{}{
			"query": "up{job=\"myapp\"}",
			"start": "-30m",
			"end":   "now",
			"step":  "60s",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "matrix") {
		t.Errorf("expected matrix in response, got: %s", result.Content)
	}
}

func TestPrometheusExecutor_MissingQuery(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewPrometheusExecutor("http://localhost:9090", "", "", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-3",
		Name:  "prometheus_query",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing query")
	}
}

func TestPrometheusExecutor_UnknownTool(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewPrometheusExecutor("http://localhost:9090", "", "", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-4",
		Name: "unknown_tool",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unknown tool")
	}
}

func TestPrometheusExecutor_BasicAuth(t *testing.T) {
	var receivedUser, receivedPass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUser, receivedPass, _ = r.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "vector",
				"result":     []interface{}{},
			},
		})
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewPrometheusExecutor(srv.URL, "admin", "secret", logger)

	_, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-5",
		Name: "prometheus_query",
		Input: map[string]interface{}{
			"query": "up",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if receivedUser != "admin" || receivedPass != "secret" {
		t.Errorf("expected admin:secret, got %s:%s", receivedUser, receivedPass)
	}
}

func newTestPrometheusFullServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/labels"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data":   []string{"__name__", "job", "instance", "namespace"},
			})

		case strings.Contains(r.URL.Path, "/api/v1/label/") && strings.HasSuffix(r.URL.Path, "/values"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data":   []string{"node-exporter", "prometheus", "myapp"},
			})

		case strings.HasSuffix(r.URL.Path, "/api/v1/series"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": []map[string]string{
					{"__name__": "up", "job": "myapp"},
					{"__name__": "node_cpu_seconds_total", "job": "node-exporter"},
					{"__name__": "up", "job": "prometheus"},
				},
			})

		case strings.HasSuffix(r.URL.Path, "/api/v1/query"):
			query := r.URL.Query().Get("query")
			if query == "" {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"status": "error",
					"error":  "missing query",
				})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "vector",
					"result": []map[string]interface{}{
						{
							"metric": map[string]string{"__name__": "up", "job": "myapp"},
							"value":  []interface{}{1234567890.0, "1"},
						},
					},
				},
			})

		case strings.HasSuffix(r.URL.Path, "/api/v1/query_range"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "matrix",
					"result": []map[string]interface{}{
						{
							"metric": map[string]string{"__name__": "up", "job": "myapp"},
							"values": [][]interface{}{{1234567890.0, "1"}},
						},
					},
				},
			})

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func TestPrometheusExecutor_ExecLabels(t *testing.T) {
	srv := newTestPrometheusFullServer(t)
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewPrometheusExecutor(srv.URL, "", "", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-labels",
		Name:  "prometheus_labels",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Available labels") {
		t.Errorf("expected 'Available labels' in result, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "job") {
		t.Errorf("expected 'job' in result, got: %s", result.Content)
	}
}

func TestPrometheusExecutor_ExecLabelValues(t *testing.T) {
	srv := newTestPrometheusFullServer(t)
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewPrometheusExecutor(srv.URL, "", "", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-lv",
		Name: "prometheus_label_values",
		Input: map[string]interface{}{
			"label": "job",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Values for") {
		t.Errorf("expected 'Values for' in result, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "myapp") {
		t.Errorf("expected 'myapp' in result, got: %s", result.Content)
	}
}

func TestPrometheusExecutor_ExecLabelValues_MissingLabel(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewPrometheusExecutor("http://localhost:9090", "", "", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-lv-missing",
		Name:  "prometheus_label_values",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing label")
	}
	if !strings.Contains(result.Content, "missing required parameter: label") {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestPrometheusExecutor_ExecSeries(t *testing.T) {
	srv := newTestPrometheusFullServer(t)
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewPrometheusExecutor(srv.URL, "", "", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-series",
		Name: "prometheus_series",
		Input: map[string]interface{}{
			"match": `{job="myapp"}`,
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Found") {
		t.Errorf("expected 'Found' in result, got: %s", result.Content)
	}
}

func TestPrometheusExecutor_ExecSeries_MissingMatch(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewPrometheusExecutor("http://localhost:9090", "", "", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-series-missing",
		Name:  "prometheus_series",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing match")
	}
	if !strings.Contains(result.Content, "missing required parameter: match") {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestPrometheusExecutor_RangeQuery_MissingParams(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewPrometheusExecutor("http://localhost:9090", "", "", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-range-missing",
		Name: "prometheus_query_range",
		Input: map[string]interface{}{
			"query": "up",
			// missing start, end, step
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing required parameters")
	}
	if !strings.Contains(result.Content, "missing required parameters") {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestPrometheusExecutor_RangeQuery_InvalidStartTime(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewPrometheusExecutor("http://localhost:9090", "", "", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-range-badtime",
		Name: "prometheus_query_range",
		Input: map[string]interface{}{
			"query": "up",
			"start": "invalid-time",
			"end":   "now",
			"step":  "60s",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for invalid start time")
	}
	if !strings.Contains(result.Content, "invalid start time") {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestFormatMetric(t *testing.T) {
	tests := []struct {
		name     string
		metric   map[string]string
		contains string
	}{
		{"empty", map[string]string{}, "{}"},
		{"with name", map[string]string{"__name__": "up", "job": "myapp"}, "up{"},
		{"without name", map[string]string{"job": "myapp"}, `{job="myapp"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatMetric(tt.metric)
			if !strings.Contains(result, tt.contains) {
				t.Errorf("formatMetric(%v) = %q, expected to contain %q", tt.metric, result, tt.contains)
			}
		})
	}
}

func TestFormatPrometheusResponse_Error(t *testing.T) {
	data := map[string]interface{}{
		"status": "error",
		"error":  "some prometheus error",
	}
	body, _ := json.Marshal(data)
	result, err := formatPrometheusResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Prometheus error") {
		t.Errorf("expected 'Prometheus error' in result, got: %s", result)
	}
}

func TestFormatPrometheusResponse_InvalidJSON(t *testing.T) {
	_, err := formatPrometheusResponse([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseRelativeTime(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		input    string
		expected time.Time
		wantErr  bool
	}{
		{"now", now, false},
		{"-30m", now.Add(-30 * time.Minute), false},
		{"-1h", now.Add(-1 * time.Hour), false},
		{"-2h30m", now.Add(-2*time.Hour - 30*time.Minute), false},
		{"2024-01-15T10:00:00Z", time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), false},
		{"invalid", time.Time{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := parseRelativeTime(tt.input, now)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseRelativeTime(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && !result.Equal(tt.expected) {
				t.Errorf("parseRelativeTime(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
