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

func newTestAzureExecutor(tokenServer, apiServer *httptest.Server) *AzureMonitorExecutor {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	exec := NewAzureMonitorExecutor("test-tenant", "test-client", "test-secret", "test-sub", logger)
	exec.client = apiServer.Client()

	// Pre-set token so tests that only test API calls don't need the token server.
	exec.accessToken = "test-token"
	exec.tokenExpiry = time.Now().Add(1 * time.Hour) // far enough in the future

	return exec
}

func TestListTools(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	exec := NewAzureMonitorExecutor("t", "c", "s", "sub", logger)

	tools, err := exec.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, expected := range []string{"azure_monitor_metrics", "azure_monitor_alerts", "azure_log_analytics", "azure_vm_status"} {
		if !names[expected] {
			t.Errorf("missing tool: %s", expected)
		}
	}
}

func TestExecuteUnknownTool(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	exec := NewAzureMonitorExecutor("t", "c", "s", "sub", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "nonexistent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for unknown tool")
	}
	if !strings.Contains(result.Content, "unknown tool") {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestQueryMetrics(t *testing.T) {
	metricsResp := map[string]interface{}{
		"value": []map[string]interface{}{
			{
				"name": map[string]string{
					"value":          "Percentage CPU",
					"localizedValue": "Percentage CPU",
				},
				"unit": "Percent",
				"timeseries": []map[string]interface{}{
					{
						"data": []map[string]interface{}{
							{
								"timeStamp": "2025-01-01T00:00:00Z",
								"average":   45.3,
								"maximum":   89.1,
							},
							{
								"timeStamp": "2025-01-01T00:05:00Z",
								"average":   32.1,
								"minimum":   10.5,
							},
						},
					},
				},
			},
		},
	}
	respBody, _ := json.Marshal(metricsResp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "Microsoft.Insights/metrics") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("metricnames") != "Percentage CPU" {
			t.Errorf("unexpected metricnames: %s", r.URL.Query().Get("metricnames"))
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(respBody)
	}))
	defer server.Close()

	exec := newTestAzureExecutor(nil, server)
	// Override management URL by replacing in the call.
	// We need to intercept the URL — patch the executor to use the test server.
	// Since the executor constructs URLs with management.azure.com, we use the server directly
	// by manipulating the transport.
	exec.client = server.Client()
	// Override the URL construction by testing via the format function directly
	// and also testing the full flow with a custom transport.
	exec.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		// Redirect all requests to the test server.
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(req)
	})

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "m1",
		Name: "azure_monitor_metrics",
		Input: map[string]interface{}{
			"resource_uri": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/myVM",
			"metric_names": "Percentage CPU",
			"timespan":     "PT1H",
			"interval":     "PT5M",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Percentage CPU") {
		t.Errorf("expected Percentage CPU in output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "avg=45.30") {
		t.Errorf("expected avg=45.30 in output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "max=89.10") {
		t.Errorf("expected max=89.10 in output, got: %s", result.Content)
	}
}

func TestQueryMetricsMissingParams(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	exec := NewAzureMonitorExecutor("t", "c", "s", "sub", logger)
	exec.accessToken = "test"
	exec.tokenExpiry = exec.tokenExpiry.Add(1<<63 - 1)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "m2",
		Name:  "azure_monitor_metrics",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing params")
	}
	if !strings.Contains(result.Content, "missing required parameters") {
		t.Errorf("unexpected error: %s", result.Content)
	}
}

func TestListAlerts(t *testing.T) {
	alertsResp := map[string]interface{}{
		"value": []map[string]interface{}{
			{
				"id":   "/subscriptions/sub/providers/Microsoft.AlertsManagement/alerts/alert1",
				"name": "HighCPUAlert",
				"properties": map[string]interface{}{
					"severity":         "Sev1",
					"monitorCondition": "Fired",
					"alertState":       "New",
					"description":      "CPU above 90%",
					"targetResource":   "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1",
					"startDateTime":    "2025-01-01T12:00:00Z",
				},
			},
		},
	}
	respBody, _ := json.Marshal(alertsResp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "Microsoft.AlertsManagement/alerts") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(respBody)
	}))
	defer server.Close()

	exec := newTestAzureExecutor(nil, server)
	exec.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(req)
	})

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "a1",
		Name: "azure_monitor_alerts",
		Input: map[string]interface{}{
			"severity":          "Sev1",
			"monitor_condition": "Fired",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "HighCPUAlert") {
		t.Errorf("expected HighCPUAlert in output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Sev1") {
		t.Errorf("expected Sev1 in output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "CPU above 90%") {
		t.Errorf("expected description in output, got: %s", result.Content)
	}
}

func TestQueryLogAnalytics(t *testing.T) {
	logResp := map[string]interface{}{
		"tables": []map[string]interface{}{
			{
				"name": "PrimaryResult",
				"columns": []map[string]string{
					{"name": "TimeGenerated", "type": "datetime"},
					{"name": "Level", "type": "string"},
					{"name": "OperationName", "type": "string"},
				},
				"rows": [][]interface{}{
					{"2025-01-01T00:00:00Z", "Error", "Delete VM"},
					{"2025-01-01T01:00:00Z", "Error", "Restart VM"},
				},
			},
		},
	}
	respBody, _ := json.Marshal(logResp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/v1/workspaces/ws-123/query") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content-type: %s", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(respBody)
	}))
	defer server.Close()

	exec := newTestAzureExecutor(nil, server)
	exec.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(req)
	})

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "l1",
		Name: "azure_log_analytics",
		Input: map[string]interface{}{
			"query":        "AzureActivity | where Level == 'Error' | take 50",
			"workspace_id": "ws-123",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "PrimaryResult") {
		t.Errorf("expected table name in output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Delete VM") {
		t.Errorf("expected row data in output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "2 rows") {
		t.Errorf("expected row count in output, got: %s", result.Content)
	}
}

func TestQueryLogAnalyticsMissingParams(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	exec := NewAzureMonitorExecutor("t", "c", "s", "sub", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "l2",
		Name: "azure_log_analytics",
		Input: map[string]interface{}{
			"query": "test",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing workspace_id")
	}
}

func TestVMStatus(t *testing.T) {
	vmResp := map[string]interface{}{
		"statuses": []map[string]interface{}{
			{
				"code":          "ProvisioningState/succeeded",
				"level":         "Info",
				"displayStatus": "Provisioning succeeded",
				"time":          "2025-01-01T00:00:00Z",
			},
			{
				"code":          "PowerState/running",
				"level":         "Info",
				"displayStatus": "VM running",
			},
		},
		"vmAgent": map[string]interface{}{
			"vmAgentVersion": "2.10.0.1",
			"statuses": []map[string]interface{}{
				{
					"code":          "ProvisioningState/succeeded",
					"level":         "Info",
					"displayStatus": "Ready",
					"message":       "GuestAgent is running",
				},
			},
		},
	}
	respBody, _ := json.Marshal(vmResp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "instanceView") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if !strings.Contains(r.URL.Path, "myRG") || !strings.Contains(r.URL.Path, "myVM") {
			t.Errorf("expected resource group and VM name in path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(respBody)
	}))
	defer server.Close()

	exec := newTestAzureExecutor(nil, server)
	exec.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(req)
	})

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "v1",
		Name: "azure_vm_status",
		Input: map[string]interface{}{
			"resource_group": "myRG",
			"vm_name":        "myVM",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "VM running") {
		t.Errorf("expected VM running in output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Provisioning succeeded") {
		t.Errorf("expected provisioning status in output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "2.10.0.1") {
		t.Errorf("expected agent version in output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "GuestAgent is running") {
		t.Errorf("expected agent message in output, got: %s", result.Content)
	}
}

func TestVMStatusMissingParams(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	exec := NewAzureMonitorExecutor("t", "c", "s", "sub", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "v2",
		Name: "azure_vm_status",
		Input: map[string]interface{}{
			"resource_group": "rg",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing vm_name")
	}
}

func TestEnsureToken(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "test-tenant") {
			t.Errorf("expected tenant in path: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form error: %v", err)
		}
		if r.Form.Get("grant_type") != "client_credentials" {
			t.Errorf("unexpected grant_type: %s", r.Form.Get("grant_type"))
		}
		if r.Form.Get("client_id") != "test-client" {
			t.Errorf("unexpected client_id: %s", r.Form.Get("client_id"))
		}

		resp := azureTokenResponse{
			AccessToken: "fresh-token",
			ExpiresIn:   3600,
			TokenType:   "Bearer",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer tokenServer.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	exec := NewAzureMonitorExecutor("test-tenant", "test-client", "test-secret", "test-sub", logger)
	exec.client = tokenServer.Client()
	exec.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(tokenServer.URL, "http://")
		return http.DefaultTransport.RoundTrip(req)
	})

	err := exec.ensureToken(context.Background())
	if err != nil {
		t.Fatalf("ensureToken error: %v", err)
	}
	if exec.accessToken != "fresh-token" {
		t.Errorf("expected fresh-token, got: %s", exec.accessToken)
	}
}

func TestHTTPErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	exec := newTestAzureExecutor(nil, server)
	exec.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(req)
	})

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "e1",
		Name: "azure_monitor_metrics",
		Input: map[string]interface{}{
			"resource_uri": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm",
			"metric_names": "CPU",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(result.Content, "HTTP 500") {
		t.Errorf("expected HTTP 500 in content, got: %s", result.Content)
	}
}

func TestFormatEmptyMetrics(t *testing.T) {
	body, _ := json.Marshal(map[string]interface{}{"value": []interface{}{}})
	result := formatMetricsResponse(body)
	if result != "No metrics returned." {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestFormatEmptyAlerts(t *testing.T) {
	body, _ := json.Marshal(map[string]interface{}{"value": []interface{}{}})
	result := formatAlertsResponse(body)
	if result != "No active alerts found." {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestFormatEmptyLogAnalytics(t *testing.T) {
	body, _ := json.Marshal(map[string]interface{}{"tables": []interface{}{}})
	result := formatLogAnalyticsResponse(body)
	if result != "No tables returned." {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestTruncate(t *testing.T) {
	if truncate("short", 10) != "short" {
		t.Error("should not truncate short strings")
	}
	result := truncate("this is a long string", 10)
	if result != "this is a ..." {
		t.Errorf("unexpected truncation: %s", result)
	}
}

// roundTripFunc allows using a function as an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
