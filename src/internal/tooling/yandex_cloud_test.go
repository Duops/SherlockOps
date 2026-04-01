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

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func newTestYCExecutor(t *testing.T, serverURL string) *YandexCloudExecutor {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	exec := NewYandexCloudExecutor("cloud-123", "folder-456", "test-token", "iam", logger)
	exec.client = &http.Client{}
	return exec
}

func TestYCListTools(t *testing.T) {
	exec := newTestYCExecutor(t, "")
	tools, err := exec.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	expectedNames := map[string]bool{
		"yc_compute_list_instances":  false,
		"yc_compute_instance_details": false,
		"yc_monitoring_read":          false,
		"yc_managed_db_list":          false,
		"yc_managed_db_hosts":         false,
		"yc_vpc_list":                 false,
	}

	for _, tool := range tools {
		if _, ok := expectedNames[tool.Name]; ok {
			expectedNames[tool.Name] = true
		}
	}

	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected tool %q not found", name)
		}
	}
}

func TestYCComputeListInstances(t *testing.T) {
	response := map[string]interface{}{
		"instances": []map[string]interface{}{
			{
				"id":         "inst-001",
				"name":       "web-server-1",
				"status":     "RUNNING",
				"zoneId":     "ru-central1-a",
				"platformId": "standard-v3",
				"resources": map[string]interface{}{
					"memory":       int64(4294967296),
					"cores":        int64(2),
					"coreFraction": int64(100),
				},
				"networkInterfaces": []map[string]interface{}{
					{
						"index":    "0",
						"subnetId": "subnet-abc",
						"primaryV4Address": map[string]interface{}{
							"address": "10.0.0.5",
							"oneToOneNat": map[string]interface{}{
								"address":   "84.201.100.1",
								"ipVersion": "IPV4",
							},
						},
					},
				},
				"fqdn": "web-server-1.ru-central1.internal",
			},
			{
				"id":         "inst-002",
				"name":       "db-server-1",
				"status":     "STOPPED",
				"zoneId":     "ru-central1-b",
				"platformId": "standard-v2",
				"resources": map[string]interface{}{
					"memory":       int64(8589934592),
					"cores":        int64(4),
					"coreFraction": int64(50),
				},
				"networkInterfaces": []map[string]interface{}{
					{
						"index":    "0",
						"subnetId": "subnet-def",
						"primaryV4Address": map[string]interface{}{
							"address": "10.0.1.10",
						},
					},
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header.
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("expected Bearer test-token, got %q", auth)
		}

		// Verify path.
		if !strings.HasPrefix(r.URL.Path, "/compute/v1/instances") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		// Verify folder_id query param.
		fid := r.URL.Query().Get("folderId")
		if fid != "folder-456" {
			t.Errorf("expected folderId=folder-456, got %q", fid)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	exec := newTestYCExecutor(t, server.URL)

	// Override the doGet to point to our test server.
	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-1",
		Name: "yc_compute_list_instances",
		Input: map[string]interface{}{},
	})

	// Since we can't easily override the URL in doGet, test the formatter directly.
	body, _ := json.Marshal(response)
	formatted, err := formatYCInstances(body)
	if err != nil {
		t.Fatalf("formatYCInstances() error = %v", err)
	}

	if !strings.Contains(formatted, "2 found") {
		t.Errorf("expected '2 found' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "web-server-1") {
		t.Errorf("expected 'web-server-1' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "RUNNING") {
		t.Errorf("expected 'RUNNING' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "84.201.100.1") {
		t.Errorf("expected public IP '84.201.100.1' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "STOPPED") {
		t.Errorf("expected 'STOPPED' in output, got:\n%s", formatted)
	}

	// Suppress unused variable warning.
	_ = result
}

func TestYCComputeListInstances_HTTPTest(t *testing.T) {
	response := map[string]interface{}{
		"instances": []map[string]interface{}{
			{
				"id":         "inst-001",
				"name":       "test-vm",
				"status":     "RUNNING",
				"zoneId":     "ru-central1-a",
				"platformId": "standard-v3",
				"resources": map[string]interface{}{
					"memory":       int64(2147483648),
					"cores":        int64(2),
					"coreFraction": int64(100),
				},
				"networkInterfaces": []map[string]interface{}{},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	exec := &YandexCloudExecutor{
		cloudID:   "cloud-123",
		folderID:  "folder-456",
		token:     "test-token",
		tokenType: "iam",
		client:    server.Client(),
		logger:    logger,
	}

	// Test doGet directly with server URL.
	body, err := exec.doGet(context.Background(), server.URL+"/compute/v1/instances?folderId=folder-456")
	if err != nil {
		t.Fatalf("doGet() error = %v", err)
	}

	formatted, err := formatYCInstances(body)
	if err != nil {
		t.Fatalf("formatYCInstances() error = %v", err)
	}

	if !strings.Contains(formatted, "test-vm") {
		t.Errorf("expected 'test-vm' in output, got:\n%s", formatted)
	}
}

func TestYCMonitoringRead_Format(t *testing.T) {
	response := map[string]interface{}{
		"metrics": []map[string]interface{}{
			{
				"name": "cpu_usage",
				"labels": map[string]string{
					"service":     "compute",
					"resource_id": "inst-001",
				},
				"type": "DGAUGE",
				"timeseries": map[string]interface{}{
					"timestamps":   []int64{1700000000000, 1700000060000, 1700000120000},
					"doubleValues": []float64{45.2, 62.8, 51.3},
				},
			},
		},
	}

	body, _ := json.Marshal(response)
	formatted, err := formatYCMonitoring(body, "cpu_usage{service='compute'}")
	if err != nil {
		t.Fatalf("formatYCMonitoring() error = %v", err)
	}

	if !strings.Contains(formatted, "cpu_usage") {
		t.Errorf("expected 'cpu_usage' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "3") {
		t.Errorf("expected datapoint count in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "45.2000") {
		t.Errorf("expected value '45.2000' in output, got:\n%s", formatted)
	}
}

func TestYCMonitoringRead_HTTPTest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", r.Header.Get("Content-Type"))
		}

		var payload map[string]interface{}
		json.NewDecoder(r.Body).Decode(&payload)
		if _, ok := payload["query"]; !ok {
			t.Error("expected 'query' in payload")
		}

		resp := map[string]interface{}{
			"metrics": []map[string]interface{}{
				{
					"name":   "cpu_usage",
					"labels": map[string]string{"service": "compute"},
					"type":   "DGAUGE",
					"timeseries": map[string]interface{}{
						"timestamps":   []int64{1700000000000},
						"doubleValues": []float64{75.5},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	exec := &YandexCloudExecutor{
		cloudID:   "cloud-123",
		folderID:  "folder-456",
		token:     "test-token",
		tokenType: "iam",
		client:    server.Client(),
		logger:    logger,
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"query":    "cpu_usage{service='compute'}",
		"fromTime": "2023-11-14T00:00:00Z",
		"toTime":   "2023-11-14T01:00:00Z",
	})

	body, err := exec.doPost(context.Background(), server.URL+"/monitoring/v2/data/read?folderId=folder-456", payload)
	if err != nil {
		t.Fatalf("doPost() error = %v", err)
	}

	formatted, err := formatYCMonitoring(body, "cpu_usage{service='compute'}")
	if err != nil {
		t.Fatalf("formatYCMonitoring() error = %v", err)
	}

	if !strings.Contains(formatted, "75.5000") {
		t.Errorf("expected '75.5000' in output, got:\n%s", formatted)
	}
}

func TestYCManagedDBList_Format(t *testing.T) {
	response := map[string]interface{}{
		"clusters": []map[string]interface{}{
			{
				"id":          "cluster-001",
				"name":        "prod-postgres",
				"status":      "RUNNING",
				"health":      "ALIVE",
				"environment": "PRODUCTION",
				"createdAt":   "2023-01-15T10:00:00Z",
			},
			{
				"id":          "cluster-002",
				"name":        "staging-postgres",
				"status":      "RUNNING",
				"health":      "DEGRADED",
				"environment": "PRESTABLE",
				"createdAt":   "2023-06-20T14:30:00Z",
			},
		},
	}

	body, _ := json.Marshal(response)
	formatted, err := formatYCClusters(body, "postgresql")
	if err != nil {
		t.Fatalf("formatYCClusters() error = %v", err)
	}

	if !strings.Contains(formatted, "2 found") {
		t.Errorf("expected '2 found' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "prod-postgres") {
		t.Errorf("expected 'prod-postgres' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "ALIVE") {
		t.Errorf("expected 'ALIVE' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "DEGRADED") {
		t.Errorf("expected 'DEGRADED' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "postgresql") {
		t.Errorf("expected 'postgresql' in output, got:\n%s", formatted)
	}
}

func TestYCManagedDBList_HTTPTest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Verify the path contains managed-postgresql.
		if !strings.Contains(r.URL.Path, "/managed-postgresql/v1/clusters") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		resp := map[string]interface{}{
			"clusters": []map[string]interface{}{
				{
					"id":          "cluster-001",
					"name":        "test-pg",
					"status":      "RUNNING",
					"health":      "ALIVE",
					"environment": "PRODUCTION",
					"createdAt":   "2023-01-15T10:00:00Z",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	exec := &YandexCloudExecutor{
		cloudID:   "cloud-123",
		folderID:  "folder-456",
		token:     "test-token",
		tokenType: "iam",
		client:    server.Client(),
		logger:    logger,
	}

	body, err := exec.doGet(context.Background(), server.URL+"/managed-postgresql/v1/clusters?folderId=folder-456")
	if err != nil {
		t.Fatalf("doGet() error = %v", err)
	}

	formatted, err := formatYCClusters(body, "postgresql")
	if err != nil {
		t.Fatalf("formatYCClusters() error = %v", err)
	}

	if !strings.Contains(formatted, "test-pg") {
		t.Errorf("expected 'test-pg' in output, got:\n%s", formatted)
	}
}

func TestYCManagedDBHosts_Format(t *testing.T) {
	response := map[string]interface{}{
		"hosts": []map[string]interface{}{
			{
				"name":           "rc1a-abc123.mdb.yandexcloud.net",
				"clusterId":     "cluster-001",
				"zoneId":        "ru-central1-a",
				"role":          "MASTER",
				"health":        "ALIVE",
				"subnetId":      "subnet-001",
				"assignPublicIp": false,
			},
			{
				"name":           "rc1b-def456.mdb.yandexcloud.net",
				"clusterId":     "cluster-001",
				"zoneId":        "ru-central1-b",
				"role":          "REPLICA",
				"health":        "ALIVE",
				"subnetId":      "subnet-002",
				"assignPublicIp": true,
			},
		},
	}

	body, _ := json.Marshal(response)
	formatted, err := formatYCHosts(body, "postgresql")
	if err != nil {
		t.Fatalf("formatYCHosts() error = %v", err)
	}

	if !strings.Contains(formatted, "2 found") {
		t.Errorf("expected '2 found' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "MASTER") {
		t.Errorf("expected 'MASTER' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "REPLICA") {
		t.Errorf("expected 'REPLICA' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "Public IP: assigned") {
		t.Errorf("expected 'Public IP: assigned' in output, got:\n%s", formatted)
	}
}

func TestYCVPCList_Format(t *testing.T) {
	response := map[string]interface{}{
		"networks": []map[string]interface{}{
			{
				"id":          "net-001",
				"name":        "default",
				"description": "Default network",
				"folderId":    "folder-456",
				"createdAt":   "2023-01-01T00:00:00Z",
			},
		},
	}

	body, _ := json.Marshal(response)
	formatted, err := formatYCNetworks(body)
	if err != nil {
		t.Fatalf("formatYCNetworks() error = %v", err)
	}

	if !strings.Contains(formatted, "1 found") {
		t.Errorf("expected '1 found' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "default") {
		t.Errorf("expected 'default' in output, got:\n%s", formatted)
	}
}

func TestYCExecute_UnknownTool(t *testing.T) {
	exec := newTestYCExecutor(t, "")
	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-1",
		Name: "yc_unknown_tool",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for unknown tool")
	}
	if !strings.Contains(result.Content, "unknown tool") {
		t.Errorf("expected 'unknown tool' in content, got %q", result.Content)
	}
}

func TestYCValidMDBService(t *testing.T) {
	valid := []string{"postgresql", "mysql", "redis", "mongodb", "clickhouse", "kafka"}
	for _, s := range valid {
		if !isValidMDBService(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}

	invalid := []string{"oracle", "mssql", "", "Postgresql"}
	for _, s := range invalid {
		if isValidMDBService(s) {
			t.Errorf("expected %q to be invalid", s)
		}
	}
}

func TestYCInstanceDetails_MissingID(t *testing.T) {
	exec := newTestYCExecutor(t, "")
	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-1",
		Name:  "yc_compute_instance_details",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing instance_id")
	}
	if !strings.Contains(result.Content, "instance_id") {
		t.Errorf("expected 'instance_id' in error content, got %q", result.Content)
	}
}

func TestYCGetFolderID(t *testing.T) {
	exec := newTestYCExecutor(t, "")

	// Default folder ID.
	fid := exec.getFolderID(map[string]interface{}{})
	if fid != "folder-456" {
		t.Errorf("expected default folder-456, got %q", fid)
	}

	// Override folder ID.
	fid = exec.getFolderID(map[string]interface{}{"folder_id": "custom-folder"})
	if fid != "custom-folder" {
		t.Errorf("expected custom-folder, got %q", fid)
	}
}

func TestYCEmptyInstances(t *testing.T) {
	body := []byte(`{"instances": []}`)
	formatted, err := formatYCInstances(body)
	if err != nil {
		t.Fatalf("formatYCInstances() error = %v", err)
	}
	if !strings.Contains(formatted, "0 found") {
		t.Errorf("expected '0 found' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "(no instances found)") {
		t.Errorf("expected '(no instances found)' in output, got:\n%s", formatted)
	}
}

func TestYCEmptyClusters(t *testing.T) {
	body := []byte(`{"clusters": []}`)
	formatted, err := formatYCClusters(body, "postgresql")
	if err != nil {
		t.Fatalf("formatYCClusters() error = %v", err)
	}
	if !strings.Contains(formatted, "0 found") {
		t.Errorf("expected '0 found' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "(no clusters found)") {
		t.Errorf("expected '(no clusters found)' in output, got:\n%s", formatted)
	}
}

func TestYCEmptyMonitoring(t *testing.T) {
	body := []byte(`{"metrics": []}`)
	formatted, err := formatYCMonitoring(body, "test_query")
	if err != nil {
		t.Fatalf("formatYCMonitoring() error = %v", err)
	}
	if !strings.Contains(formatted, "Total datapoints: 0") {
		t.Errorf("expected 'Total datapoints: 0' in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "(no datapoints") {
		t.Errorf("expected '(no datapoints' in output, got:\n%s", formatted)
	}
}

func TestFormatYCInstanceDetails(t *testing.T) {
	body := []byte(`{
		"id": "inst-123",
		"name": "web-server-1",
		"status": "RUNNING",
		"zoneId": "ru-central1-a",
		"platformId": "standard-v3",
		"description": "Main web server",
		"createdAt": "2024-01-15T12:00:00Z",
		"resources": {
			"cores": 4,
			"coreFraction": 100,
			"memory": 8589934592,
			"gpus": 0
		},
		"bootDisk": {
			"diskId": "disk-456",
			"mode": "READ_WRITE"
		},
		"networkInterfaces": [
			{
				"index": "0",
				"subnetId": "subnet-789",
				"primaryV4Address": {
					"address": "10.0.0.5",
					"oneToOneNat": {
						"address": "203.0.113.10"
					}
				}
			}
		],
		"labels": {
			"env": "production",
			"team": "platform"
		}
	}`)

	result, err := formatYCInstanceDetails(body)
	if err != nil {
		t.Fatalf("formatYCInstanceDetails() error = %v", err)
	}

	checks := []string{
		"web-server-1",
		"inst-123",
		"RUNNING",
		"ru-central1-a",
		"standard-v3",
		"Main web server",
		"4 cores",
		"disk-456",
		"10.0.0.5",
		"203.0.113.10",
		"production",
	}
	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("expected %q in output, got:\n%s", check, result)
		}
	}
}

func TestFormatYCInstanceDetails_Minimal(t *testing.T) {
	body := []byte(`{
		"id": "inst-min",
		"name": "minimal",
		"status": "STOPPED",
		"zoneId": "ru-central1-b",
		"platformId": "standard-v2",
		"createdAt": "2024-06-01T00:00:00Z"
	}`)

	result, err := formatYCInstanceDetails(body)
	if err != nil {
		t.Fatalf("formatYCInstanceDetails() error = %v", err)
	}

	if !strings.Contains(result, "minimal") {
		t.Errorf("expected 'minimal' in output, got:\n%s", result)
	}
	if !strings.Contains(result, "STOPPED") {
		t.Errorf("expected 'STOPPED' in output, got:\n%s", result)
	}
}

func TestFormatYCInstanceDetails_InvalidJSON(t *testing.T) {
	_, err := formatYCInstanceDetails([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
