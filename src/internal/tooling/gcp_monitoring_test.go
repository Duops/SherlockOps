package tooling

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

// testSAKey generates a temporary RSA key and returns service account JSON.
func testSAKey(t *testing.T, tokenURL string) string {
	t.Helper()

	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	privDER, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privDER,
	})

	sa := map[string]string{
		"type":           "service_account",
		"client_email":   "test@test-project.iam.gserviceaccount.com",
		"private_key":    string(privPEM),
		"private_key_id": "key-id-123",
		"token_uri":      tokenURL,
	}

	data, _ := json.Marshal(sa)
	return string(data)
}

// newTestTokenServer returns an httptest.Server that issues fake access tokens.
func newTestTokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "fake-access-token-12345",
			"expires_in":   3600,
			"token_type":   "Bearer",
		})
	}))
}

// newTestGCPServer returns an httptest.Server that handles GCP API requests.
func newTestGCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify authorization header.
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.Contains(r.URL.Path, "/timeSeries:query") && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(map[string]interface{}{
				"timeSeries": []map[string]interface{}{
					{
						"metric": map[string]interface{}{
							"type":   "compute.googleapis.com/instance/cpu/utilization",
							"labels": map[string]string{},
						},
						"resource": map[string]interface{}{
							"type": "gce_instance",
							"labels": map[string]string{
								"instance_id": "123456789",
								"zone":        "us-central1-a",
							},
						},
						"points": []map[string]interface{}{
							{
								"interval": map[string]string{
									"startTime": "2024-01-15T11:00:00Z",
									"endTime":   "2024-01-15T12:00:00Z",
								},
								"value": map[string]interface{}{
									"doubleValue": 0.42,
								},
							},
							{
								"interval": map[string]string{
									"startTime": "2024-01-15T10:00:00Z",
									"endTime":   "2024-01-15T11:00:00Z",
								},
								"value": map[string]interface{}{
									"doubleValue": 0.35,
								},
							},
						},
					},
				},
			})

		case strings.Contains(r.URL.Path, "/alertPolicies"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"alertPolicies": []map[string]interface{}{
					{
						"name":        "projects/test-project/alertPolicies/123",
						"displayName": "High CPU Usage",
						"enabled":     map[string]interface{}{"value": true},
						"conditions": []map[string]interface{}{
							{
								"displayName": "CPU > 80%",
								"name":        "projects/test-project/alertPolicies/123/conditions/456",
							},
						},
						"documentation": map[string]interface{}{
							"content": "Alert when CPU usage exceeds 80%.",
						},
					},
					{
						"name":        "projects/test-project/alertPolicies/789",
						"displayName": "Disk Full",
						"enabled":     map[string]interface{}{"value": false},
						"conditions": []map[string]interface{}{
							{
								"displayName": "Disk > 90%",
								"name":        "projects/test-project/alertPolicies/789/conditions/012",
							},
						},
					},
				},
			})

		case strings.Contains(r.URL.Path, "/aggregated/instances"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"items": map[string]interface{}{
					"zones/us-central1-a": map[string]interface{}{
						"instances": []map[string]interface{}{
							{
								"name":        "web-server-1",
								"status":      "RUNNING",
								"machineType": "zones/us-central1-a/machineTypes/e2-medium",
								"zone":        "projects/test-project/zones/us-central1-a",
								"networkInterfaces": []map[string]interface{}{
									{
										"networkIP": "10.128.0.2",
										"accessConfigs": []map[string]interface{}{
											{"natIP": "35.202.100.1"},
										},
									},
								},
							},
						},
					},
				},
			})

		case strings.Contains(r.URL.Path, "/zones/") && strings.Contains(r.URL.Path, "/instances"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []map[string]interface{}{
					{
						"name":        "db-server-1",
						"status":      "RUNNING",
						"machineType": "zones/us-central1-a/machineTypes/n2-standard-4",
						"zone":        "projects/test-project/zones/us-central1-a",
						"networkInterfaces": []map[string]interface{}{
							{
								"networkIP":     "10.128.0.5",
								"accessConfigs": []map[string]interface{}{},
							},
						},
					},
				},
			})

		case strings.Contains(r.URL.Path, "/entries:list"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"entries": []map[string]interface{}{
					{
						"timestamp":   "2024-01-15T12:00:00Z",
						"severity":    "ERROR",
						"logName":     "projects/test-project/logs/syslog",
						"textPayload": "disk /dev/sda1 is 95% full",
						"resource": map[string]interface{}{
							"type": "gce_instance",
							"labels": map[string]string{
								"instance_id": "123456789",
							},
						},
					},
					{
						"timestamp": "2024-01-15T11:55:00Z",
						"severity":  "ERROR",
						"logName":   "projects/test-project/logs/app",
						"jsonPayload": map[string]interface{}{
							"message": "connection refused",
							"code":    500,
						},
						"resource": map[string]interface{}{
							"type":   "gce_instance",
							"labels": map[string]string{},
						},
					},
				},
			})

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func newTestGCPExecutor(t *testing.T, tokenSrv, gcpSrv *httptest.Server) *GCPMonitoringExecutor {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	saJSON := testSAKey(t, tokenSrv.URL)

	exec := NewGCPMonitoringExecutor("test-project", saJSON, logger)
	exec.tokenURL = tokenSrv.URL
	exec.monitoringV3 = gcpSrv.URL
	exec.computeV1 = gcpSrv.URL
	exec.loggingV2 = gcpSrv.URL
	return exec
}

func TestGCPMonitoringExecutor_ListTools(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewGCPMonitoringExecutor("test-project", "{}", logger)

	tools, err := exec.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	expected := []string{
		"gcp_monitoring_query",
		"gcp_monitoring_list_alerts",
		"gcp_logging_query",
		"gcp_compute_instances",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestGCPMonitoringExecutor_UnknownTool(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewGCPMonitoringExecutor("test-project", "{}", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-1",
		Name: "unknown_tool",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unknown tool")
	}
	if !strings.Contains(result.Content, "unknown tool") {
		t.Errorf("unexpected error message: %s", result.Content)
	}
}

func TestGCPMonitoringExecutor_TokenGeneration(t *testing.T) {
	tokenSrv := newTestTokenServer(t)
	defer tokenSrv.Close()

	gcpSrv := newTestGCPServer(t)
	defer gcpSrv.Close()

	exec := newTestGCPExecutor(t, tokenSrv, gcpSrv)

	// First call should fetch a token.
	token, err := exec.ensureToken(context.Background())
	if err != nil {
		t.Fatalf("ensureToken error: %v", err)
	}
	if token != "fake-access-token-12345" {
		t.Errorf("unexpected token: %s", token)
	}

	// Second call should return the cached token.
	token2, err := exec.ensureToken(context.Background())
	if err != nil {
		t.Fatalf("ensureToken error on second call: %v", err)
	}
	if token2 != token {
		t.Errorf("expected cached token, got different value")
	}
}

func TestGCPMonitoringExecutor_QueryTimeSeries(t *testing.T) {
	tokenSrv := newTestTokenServer(t)
	defer tokenSrv.Close()

	gcpSrv := newTestGCPServer(t)
	defer gcpSrv.Close()

	exec := newTestGCPExecutor(t, tokenSrv, gcpSrv)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-ts",
		Name: "gcp_monitoring_query",
		Input: map[string]interface{}{
			"query": "fetch gce_instance :: compute.googleapis.com/instance/cpu/utilization | within 30m",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "cpu/utilization") {
		t.Errorf("expected cpu/utilization in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "0.42") {
		t.Errorf("expected value 0.42 in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "gce_instance") {
		t.Errorf("expected gce_instance in response, got: %s", result.Content)
	}
}

func TestGCPMonitoringExecutor_QueryTimeSeries_MissingQuery(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewGCPMonitoringExecutor("test-project", "{}", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-nq",
		Name:  "gcp_monitoring_query",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing query")
	}
	if !strings.Contains(result.Content, "missing required parameter: query") {
		t.Errorf("unexpected error message: %s", result.Content)
	}
}

func TestGCPMonitoringExecutor_ListAlertPolicies(t *testing.T) {
	tokenSrv := newTestTokenServer(t)
	defer tokenSrv.Close()

	gcpSrv := newTestGCPServer(t)
	defer gcpSrv.Close()

	exec := newTestGCPExecutor(t, tokenSrv, gcpSrv)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-ap",
		Name:  "gcp_monitoring_list_alerts",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "High CPU Usage") {
		t.Errorf("expected 'High CPU Usage' in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "ENABLED") {
		t.Errorf("expected ENABLED in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Disk Full") {
		t.Errorf("expected 'Disk Full' in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "DISABLED") {
		t.Errorf("expected DISABLED in response, got: %s", result.Content)
	}
}

func TestGCPMonitoringExecutor_ListInstances_AllZones(t *testing.T) {
	tokenSrv := newTestTokenServer(t)
	defer tokenSrv.Close()

	gcpSrv := newTestGCPServer(t)
	defer gcpSrv.Close()

	exec := newTestGCPExecutor(t, tokenSrv, gcpSrv)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-inst-all",
		Name:  "gcp_compute_instances",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "web-server-1") {
		t.Errorf("expected 'web-server-1' in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "RUNNING") {
		t.Errorf("expected RUNNING in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "35.202.100.1") {
		t.Errorf("expected external IP in response, got: %s", result.Content)
	}
}

func TestGCPMonitoringExecutor_ListInstances_SpecificZone(t *testing.T) {
	tokenSrv := newTestTokenServer(t)
	defer tokenSrv.Close()

	gcpSrv := newTestGCPServer(t)
	defer gcpSrv.Close()

	exec := newTestGCPExecutor(t, tokenSrv, gcpSrv)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-inst-zone",
		Name: "gcp_compute_instances",
		Input: map[string]interface{}{
			"zone": "us-central1-a",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "db-server-1") {
		t.Errorf("expected 'db-server-1' in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "n2-standard-4") {
		t.Errorf("expected machine type in response, got: %s", result.Content)
	}
}

func TestGCPMonitoringExecutor_QueryLogs(t *testing.T) {
	tokenSrv := newTestTokenServer(t)
	defer tokenSrv.Close()

	gcpSrv := newTestGCPServer(t)
	defer gcpSrv.Close()

	exec := newTestGCPExecutor(t, tokenSrv, gcpSrv)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-logs",
		Name: "gcp_logging_query",
		Input: map[string]interface{}{
			"filter":   "resource.type=\"gce_instance\" severity>=ERROR",
			"order_by": "timestamp desc",
			"page_size": float64(10),
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "ERROR") {
		t.Errorf("expected ERROR severity in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "disk /dev/sda1 is 95% full") {
		t.Errorf("expected log text in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "connection refused") {
		t.Errorf("expected JSON payload in response, got: %s", result.Content)
	}
}

func TestGCPMonitoringExecutor_QueryLogs_MissingFilter(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewGCPMonitoringExecutor("test-project", "{}", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-logs-nf",
		Name:  "gcp_logging_query",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing filter")
	}
}

func TestGCPMonitoringExecutor_LoadServiceAccountFromFile(t *testing.T) {
	tokenSrv := newTestTokenServer(t)
	defer tokenSrv.Close()

	saJSON := testSAKey(t, tokenSrv.URL)

	tmpFile, err := os.CreateTemp(t.TempDir(), "sa-*.json")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := tmpFile.WriteString(saJSON); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	tmpFile.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewGCPMonitoringExecutor("test-project", tmpFile.Name(), logger)
	exec.tokenURL = tokenSrv.URL

	token, err := exec.ensureToken(context.Background())
	if err != nil {
		t.Fatalf("ensureToken error: %v", err)
	}
	if token != "fake-access-token-12345" {
		t.Errorf("unexpected token: %s", token)
	}
}

func TestCreateSignedJWT(t *testing.T) {
	tokenSrv := newTestTokenServer(t)
	defer tokenSrv.Close()

	saJSON := testSAKey(t, tokenSrv.URL)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewGCPMonitoringExecutor("test-project", saJSON, logger)
	exec.tokenURL = tokenSrv.URL

	saKey, err := exec.loadServiceAccountKey()
	if err != nil {
		t.Fatalf("loadServiceAccountKey error: %v", err)
	}

	now := saKey.ClientEmail // Just to verify it loaded.
	if now != "test@test-project.iam.gserviceaccount.com" {
		t.Errorf("unexpected client email: %s", now)
	}

	jwt, err := exec.createSignedJWT(saKey, fixedTime(), fixedTime().Add(1*3600*1e9))
	if err != nil {
		t.Fatalf("createSignedJWT error: %v", err)
	}

	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}

	// Verify header.
	headerJSON, err := base64RawURLDecode(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var header map[string]string
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatalf("parse header: %v", err)
	}
	if header["alg"] != "RS256" {
		t.Errorf("expected RS256, got %s", header["alg"])
	}
	if header["typ"] != "JWT" {
		t.Errorf("expected JWT, got %s", header["typ"])
	}

	// Verify claims.
	claimsJSON, err := base64RawURLDecode(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatalf("parse claims: %v", err)
	}
	if claims["iss"] != "test@test-project.iam.gserviceaccount.com" {
		t.Errorf("unexpected iss: %v", claims["iss"])
	}
	if claims["scope"] != gcpScope {
		t.Errorf("unexpected scope: %v", claims["scope"])
	}
}

func fixedTime() time.Time {
	return time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
}

func base64RawURLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func TestFormatPointValue(t *testing.T) {
	dv := 3.14
	iv := "42"
	bv := true
	sv := "hello"

	tests := []struct {
		name     string
		dv       *float64
		iv       *string
		bv       *bool
		sv       *string
		expected string
	}{
		{"double", &dv, nil, nil, nil, "3.14"},
		{"int64", nil, &iv, nil, nil, "42"},
		{"bool", nil, nil, &bv, nil, "true"},
		{"string", nil, nil, nil, &sv, "hello"},
		{"null", nil, nil, nil, nil, "null"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatPointValue(tt.dv, tt.iv, tt.bv, tt.sv)
			if result != tt.expected {
				t.Errorf("formatPointValue() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestLastSegment(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"projects/myproject/zones/us-central1-a", "us-central1-a"},
		{"no-slash", "no-slash"},
		{"trailing/", ""},
		{"a/b/c", "c"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := lastSegment(tt.input)
			if result != tt.expected {
				t.Errorf("lastSegment(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGcpTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is a long string", 10, "this is a ..."},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := gcpTruncate(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("gcpTruncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
			}
		})
	}
}
