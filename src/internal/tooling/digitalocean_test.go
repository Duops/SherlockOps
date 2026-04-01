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

// ---------------------------------------------------------------------------
// ListTools test
// ---------------------------------------------------------------------------

func TestDigitalOceanExecutor_ListTools(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewDigitalOceanExecutor("test-token", logger)

	tools, err := exec.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if len(tools) != 6 {
		t.Fatalf("expected 6 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	expected := []string{
		"do_list_droplets",
		"do_droplet_details",
		"do_monitoring_metrics",
		"do_list_kubernetes_clusters",
		"do_list_databases",
		"do_list_alerts",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestDigitalOceanExecutor_UnknownTool(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewDigitalOceanExecutor("test-token", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-1",
		Name: "nonexistent_tool",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unknown tool")
	}
}

// ---------------------------------------------------------------------------
// ListDroplets test
// ---------------------------------------------------------------------------

func TestDigitalOceanExecutor_ListDroplets(t *testing.T) {
	srv := newTestDOServer(t)
	defer srv.Close()

	exec := newTestDOExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-droplets",
		Name: "do_list_droplets",
		Input: map[string]interface{}{
			"tag_name": "web",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "web-01") {
		t.Errorf("expected droplet name in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "active") {
		t.Errorf("expected active status, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "10.0.0.1") {
		t.Errorf("expected private IP, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Droplets: 1") {
		t.Errorf("expected 1 droplet, got: %s", result.Content)
	}
}

func TestDigitalOceanExecutor_ListDroplets_NoFilter(t *testing.T) {
	srv := newTestDOServer(t)
	defer srv.Close()

	exec := newTestDOExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-droplets-all",
		Name:  "do_list_droplets",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// DropletDetails test
// ---------------------------------------------------------------------------

func TestDigitalOceanExecutor_DropletDetails(t *testing.T) {
	srv := newTestDOServer(t)
	defer srv.Close()

	exec := newTestDOExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-detail",
		Name: "do_droplet_details",
		Input: map[string]interface{}{
			"droplet_id": "12345",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "web-01") {
		t.Errorf("expected droplet name, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "vCPUs: 2") {
		t.Errorf("expected vCPUs info, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Memory: 2048 MB") {
		t.Errorf("expected memory info, got: %s", result.Content)
	}
}

func TestDigitalOceanExecutor_DropletDetails_MissingID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewDigitalOceanExecutor("test-token", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-detail-missing",
		Name:  "do_droplet_details",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing droplet_id")
	}
}

// ---------------------------------------------------------------------------
// MonitoringMetrics test
// ---------------------------------------------------------------------------

func TestDigitalOceanExecutor_MonitoringMetrics(t *testing.T) {
	srv := newTestDOServer(t)
	defer srv.Close()

	exec := newTestDOExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-metrics",
		Name: "do_monitoring_metrics",
		Input: map[string]interface{}{
			"host_id": "12345",
			"metric":  "cpu",
			"start":   "-1h",
			"end":     "now",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "cpu") {
		t.Errorf("expected cpu metric in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Datapoints: 2") {
		t.Errorf("expected 2 datapoints, got: %s", result.Content)
	}
}

func TestDigitalOceanExecutor_MonitoringMetrics_MissingParams(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewDigitalOceanExecutor("test-token", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-metrics-missing",
		Name: "do_monitoring_metrics",
		Input: map[string]interface{}{
			"host_id": "12345",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing params")
	}
}

func TestDigitalOceanExecutor_MonitoringMetrics_UnsupportedMetric(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewDigitalOceanExecutor("test-token", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-metrics-bad",
		Name: "do_monitoring_metrics",
		Input: map[string]interface{}{
			"host_id": "12345",
			"metric":  "unknown_metric",
			"start":   "-1h",
			"end":     "now",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unsupported metric")
	}
	if !strings.Contains(result.Content, "unsupported metric") {
		t.Errorf("expected unsupported metric message, got: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// ListKubernetesClusters test
// ---------------------------------------------------------------------------

func TestDigitalOceanExecutor_ListKubernetesClusters(t *testing.T) {
	srv := newTestDOServer(t)
	defer srv.Close()

	exec := newTestDOExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-k8s",
		Name:  "do_list_kubernetes_clusters",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "prod-cluster") {
		t.Errorf("expected cluster name, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "running") {
		t.Errorf("expected running status, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Nodes: 3") {
		t.Errorf("expected 3 nodes, got: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// ListDatabases test
// ---------------------------------------------------------------------------

func TestDigitalOceanExecutor_ListDatabases(t *testing.T) {
	srv := newTestDOServer(t)
	defer srv.Close()

	exec := newTestDOExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-dbs",
		Name:  "do_list_databases",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "prod-pg") {
		t.Errorf("expected database name, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "pg") {
		t.Errorf("expected pg engine, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "online") {
		t.Errorf("expected online status, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "db-prod.example.com") {
		t.Errorf("expected connection host, got: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// ListAlerts test
// ---------------------------------------------------------------------------

func TestDigitalOceanExecutor_ListAlerts(t *testing.T) {
	srv := newTestDOServer(t)
	defer srv.Close()

	exec := newTestDOExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-alerts",
		Name:  "do_list_alerts",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "v1/insights/droplet/cpu") {
		t.Errorf("expected alert type, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "CPU usage exceeds 80%") {
		t.Errorf("expected alert description, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Enabled: true") {
		t.Errorf("expected enabled status, got: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// Format function tests
// ---------------------------------------------------------------------------

func TestFormatDroplets_Empty(t *testing.T) {
	result := formatDroplets(nil)
	if !strings.Contains(result, "0 found") {
		t.Errorf("expected 0 found, got: %s", result)
	}
	if !strings.Contains(result, "no droplets") {
		t.Errorf("expected no droplets message, got: %s", result)
	}
}

func TestFormatK8sClusters_Empty(t *testing.T) {
	result := formatK8sClusters(nil)
	if !strings.Contains(result, "0 found") {
		t.Errorf("expected 0 found, got: %s", result)
	}
	if !strings.Contains(result, "no Kubernetes") {
		t.Errorf("expected no clusters message, got: %s", result)
	}
}

func TestFormatDatabases_Empty(t *testing.T) {
	result := formatDatabases(nil)
	if !strings.Contains(result, "0 found") {
		t.Errorf("expected 0 found, got: %s", result)
	}
	if !strings.Contains(result, "no managed databases") {
		t.Errorf("expected no databases message, got: %s", result)
	}
}

func TestFormatAlertPolicies_Empty(t *testing.T) {
	result := formatAlertPolicies(nil)
	if !strings.Contains(result, "0 found") {
		t.Errorf("expected 0 found, got: %s", result)
	}
	if !strings.Contains(result, "no alert policies") {
		t.Errorf("expected no policies message, got: %s", result)
	}
}

func TestFormatDOMetrics_Empty(t *testing.T) {
	resp := doMetricsResponse{Status: "success"}
	result := formatDOMetrics(resp, "cpu", "12345")
	if !strings.Contains(result, "Datapoints: 0") {
		t.Errorf("expected 0 datapoints, got: %s", result)
	}
	if !strings.Contains(result, "no datapoints") {
		t.Errorf("expected no datapoints message, got: %s", result)
	}
}

// ---------------------------------------------------------------------------
// HTTP error test
// ---------------------------------------------------------------------------

func TestDigitalOceanExecutor_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"id":"unauthorized","message":"Unable to authenticate you"}`))
	}))
	defer srv.Close()

	exec := newTestDOExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-error",
		Name:  "do_list_droplets",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for HTTP 401")
	}
	if !strings.Contains(result.Content, "401") {
		t.Errorf("expected 401 in error, got: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newTestDOExecutor(t *testing.T, serverURL string) *DigitalOceanExecutor {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewDigitalOceanExecutor("test-token", logger)
	exec.baseURL = serverURL + "/v2"
	exec.client = &http.Client{Timeout: 5 * time.Second}
	return exec
}

func newTestDOServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		path := r.URL.Path

		switch {
		case path == "/v2/droplets" && r.Method == http.MethodGet:
			handleDOListDroplets(w, r)
		case strings.HasPrefix(path, "/v2/droplets/") && r.Method == http.MethodGet:
			handleDODropletDetails(w)
		case strings.HasPrefix(path, "/v2/monitoring/metrics/droplet/"):
			handleDOMonitoringMetrics(w)
		case path == "/v2/kubernetes/clusters":
			handleDOListKubernetesClusters(w)
		case path == "/v2/databases":
			handleDOListDatabases(w)
		case path == "/v2/monitoring/alerts":
			handleDOListAlerts(w)
		default:
			http.Error(w, "not found: "+path, http.StatusNotFound)
		}
	}))
}

func handleDOListDroplets(w http.ResponseWriter, _ *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"droplets": []map[string]interface{}{
			{
				"id":     12345,
				"name":   "web-01",
				"status": "active",
				"region": map[string]interface{}{
					"slug": "nyc1",
					"name": "New York 1",
				},
				"size": map[string]interface{}{
					"slug":   "s-2vcpu-2gb",
					"vcpus":  2,
					"memory": 2048,
					"disk":   60,
				},
				"networks": map[string]interface{}{
					"v4": []map[string]interface{}{
						{"ip_address": "10.0.0.1", "type": "private"},
						{"ip_address": "203.0.113.1", "type": "public"},
					},
				},
				"image": map[string]interface{}{
					"name": "Ubuntu 22.04",
					"slug": "ubuntu-22-04-x64",
				},
				"tags":       []string{"web", "prod"},
				"created_at": "2024-01-01T00:00:00Z",
			},
		},
		"meta": map[string]interface{}{"total": 1},
	})
}

func handleDODropletDetails(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"droplet": map[string]interface{}{
			"id":     12345,
			"name":   "web-01",
			"status": "active",
			"region": map[string]interface{}{
				"slug": "nyc1",
				"name": "New York 1",
			},
			"size": map[string]interface{}{
				"slug":   "s-2vcpu-2gb",
				"vcpus":  2,
				"memory": 2048,
				"disk":   60,
			},
			"networks": map[string]interface{}{
				"v4": []map[string]interface{}{
					{"ip_address": "10.0.0.1", "type": "private"},
					{"ip_address": "203.0.113.1", "type": "public"},
				},
			},
			"image": map[string]interface{}{
				"name": "Ubuntu 22.04",
				"slug": "ubuntu-22-04-x64",
			},
			"tags":       []string{"web", "prod"},
			"volume_ids": []string{"vol-abc123"},
			"vpc_uuid":   "vpc-12345",
			"created_at": "2024-01-01T00:00:00Z",
		},
	})
}

func handleDOMonitoringMetrics(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"resultType": "matrix",
			"result": []map[string]interface{}{
				{
					"metric": map[string]interface{}{
						"host_id": "12345",
						"mode":    "user",
					},
					"values": [][]interface{}{
						{1705320000.0, "45.5"},
						{1705320300.0, "52.3"},
					},
				},
			},
		},
	})
}

func handleDOListKubernetesClusters(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"kubernetes_clusters": []map[string]interface{}{
			{
				"id":           "k8s-abc123",
				"name":         "prod-cluster",
				"region":       "nyc1",
				"version_slug": "1.28.2-do.0",
				"status": map[string]interface{}{
					"state":   "running",
					"message": "",
				},
				"node_pools": []map[string]interface{}{
					{
						"name":  "default-pool",
						"size":  "s-4vcpu-8gb",
						"count": 3,
					},
				},
				"created_at": "2024-01-01T00:00:00Z",
			},
		},
	})
}

func handleDOListDatabases(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"databases": []map[string]interface{}{
			{
				"id":        "db-abc123",
				"name":      "prod-pg",
				"engine":    "pg",
				"version":   "15",
				"status":    "online",
				"size":      "db-s-2vcpu-4gb",
				"region":    "nyc1",
				"num_nodes": 2,
				"connection": map[string]interface{}{
					"uri":      "postgresql://user:pass@db-prod.example.com:25060/defaultdb?sslmode=require",
					"host":     "db-prod.example.com",
					"port":     25060,
					"database": "defaultdb",
				},
				"created_at": "2024-01-01T00:00:00Z",
			},
		},
	})
}

func handleDOListAlerts(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"policies": []map[string]interface{}{
			{
				"uuid":        "alert-abc123",
				"type":        "v1/insights/droplet/cpu",
				"description": "CPU usage exceeds 80%",
				"compare":     "GreaterThan",
				"value":       80,
				"window":      "5m",
				"entities":    []string{"12345", "67890"},
				"enabled":     true,
			},
		},
	})
}
