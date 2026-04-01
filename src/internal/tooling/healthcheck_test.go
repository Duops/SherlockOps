package tooling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

// healthCheckExecutor is a mock that implements HealthChecker.
type healthCheckExecutor struct {
	tools     []domain.Tool
	listErr   error
	checkErr  error
	checkCall bool
}

func (h *healthCheckExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	if h.listErr != nil {
		return nil, h.listErr
	}
	return h.tools, nil
}

func (h *healthCheckExecutor) Execute(_ context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	return &domain.ToolResult{CallID: call.ID, Content: "ok"}, nil
}

func (h *healthCheckExecutor) HealthCheck(_ context.Context) error {
	h.checkCall = true
	return h.checkErr
}

func TestCheckHealth_HealthCheckOK(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	reg := NewRegistry(logger)

	exec := &healthCheckExecutor{
		tools: []domain.Tool{{Name: "prom_query", Description: "test"}},
	}
	reg.Register(exec)

	CheckHealth(context.Background(), reg, logger)

	if !exec.checkCall {
		t.Error("expected HealthCheck to be called")
	}
	if !strings.Contains(buf.String(), "tool health check OK") {
		t.Errorf("expected 'tool health check OK' in log, got: %s", buf.String())
	}
}

func TestCheckHealth_HealthCheckFailed(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	reg := NewRegistry(logger)

	exec := &healthCheckExecutor{
		tools:    []domain.Tool{{Name: "loki_query", Description: "test"}},
		checkErr: fmt.Errorf("connection refused"),
	}
	reg.Register(exec)

	CheckHealth(context.Background(), reg, logger)

	if !exec.checkCall {
		t.Error("expected HealthCheck to be called")
	}
	if !strings.Contains(buf.String(), "tool health check FAILED") {
		t.Errorf("expected 'tool health check FAILED' in log, got: %s", buf.String())
	}
}

func TestCheckHealth_NoHealthChecker(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	reg := NewRegistry(logger)

	exec := &mockExecutor{
		tools: []domain.Tool{{Name: "tool_a", Description: "test"}},
	}
	reg.Register(exec)

	CheckHealth(context.Background(), reg, logger)

	if !strings.Contains(buf.String(), "tool registered (no health check)") {
		t.Errorf("expected 'tool registered (no health check)' in log, got: %s", buf.String())
	}
}

func TestCheckHealth_ListToolsError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	reg := NewRegistry(logger)

	exec := &mockExecutor{listErr: fmt.Errorf("list failed")}
	reg.Register(exec)

	CheckHealth(context.Background(), reg, logger)

	if !strings.Contains(buf.String(), "cannot list tools") {
		t.Errorf("expected 'cannot list tools' in log, got: %s", buf.String())
	}
}

func TestCheckHealth_EmptyToolList(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	reg := NewRegistry(logger)

	exec := &mockExecutor{tools: []domain.Tool{}}
	reg.Register(exec)

	CheckHealth(context.Background(), reg, logger)

	if !strings.Contains(buf.String(), "unknown") {
		t.Errorf("expected 'unknown' tool name in log for empty tools, got: %s", buf.String())
	}
}

func TestCheckHealth_ToolNameParsing(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	reg := NewRegistry(logger)

	// Tool name with underscore: prefix should be extracted
	exec := &healthCheckExecutor{
		tools: []domain.Tool{{Name: "myprefix_somethingelse", Description: "test"}},
	}
	reg.Register(exec)

	CheckHealth(context.Background(), reg, logger)

	if !strings.Contains(buf.String(), "myprefix") {
		t.Errorf("expected 'myprefix' in log, got: %s", buf.String())
	}
}

// --- HealthCheck method tests using httptest servers ---

func TestPrometheusExecutor_HealthCheck_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()

	exec := NewPrometheusExecutor(srv.URL, "", "", testLogger())
	err := exec.HealthCheck(context.Background())
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestPrometheusExecutor_HealthCheck_WithBasicAuth(t *testing.T) {
	var gotUser, gotPass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	exec := NewPrometheusExecutor(srv.URL, "user", "pass", testLogger())
	err := exec.HealthCheck(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if gotUser != "user" || gotPass != "pass" {
		t.Errorf("expected user:pass, got %s:%s", gotUser, gotPass)
	}
}

func TestPrometheusExecutor_HealthCheck_NonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	exec := NewPrometheusExecutor(srv.URL, "", "", testLogger())
	err := exec.HealthCheck(context.Background())
	if err == nil {
		t.Error("expected error for non-200 response")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("expected '503' in error, got: %v", err)
	}
}

func TestPrometheusExecutor_HealthCheck_ConnectionError(t *testing.T) {
	exec := NewPrometheusExecutor("http://127.0.0.1:1", "", "", testLogger())
	err := exec.HealthCheck(context.Background())
	if err == nil {
		t.Error("expected error for connection failure")
	}
	if !strings.Contains(err.Error(), "connection failed") {
		t.Errorf("expected 'connection failed' in error, got: %v", err)
	}
}

func TestLokiExecutor_HealthCheck_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/labels" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   []string{"app", "namespace"},
		})
	}))
	defer srv.Close()

	exec := NewLokiExecutor(srv.URL, "", "", testLogger())
	err := exec.HealthCheck(context.Background())
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestLokiExecutor_HealthCheck_WithBasicAuth(t *testing.T) {
	var gotUser, gotPass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	exec := NewLokiExecutor(srv.URL, "admin", "secret", testLogger())
	err := exec.HealthCheck(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if gotUser != "admin" || gotPass != "secret" {
		t.Errorf("expected admin:secret, got %s:%s", gotUser, gotPass)
	}
}

func TestLokiExecutor_HealthCheck_NonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	exec := NewLokiExecutor(srv.URL, "", "", testLogger())
	err := exec.HealthCheck(context.Background())
	if err == nil {
		t.Error("expected error for non-200")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected '500' in error, got: %v", err)
	}
}

func TestLokiExecutor_HealthCheck_ConnectionError(t *testing.T) {
	exec := NewLokiExecutor("http://127.0.0.1:1", "", "", testLogger())
	err := exec.HealthCheck(context.Background())
	if err == nil {
		t.Error("expected error for connection failure")
	}
	if !strings.Contains(err.Error(), "connection failed") {
		t.Errorf("expected 'connection failed' in error, got: %v", err)
	}
}

func TestMCPClient_HealthCheck_NoTools(t *testing.T) {
	client := NewMCPClient("test", "http://localhost", "", "", nil, testLogger())
	err := client.HealthCheck(context.Background())
	if err == nil {
		t.Error("expected error for no tools")
	}
	if !strings.Contains(err.Error(), "no tools discovered") {
		t.Errorf("expected 'no tools discovered', got: %v", err)
	}
}

func TestMCPClient_HealthCheck_WithTools(t *testing.T) {
	client := NewMCPClient("test", "http://localhost", "", "", nil, testLogger())
	client.tools = []domain.Tool{{Name: "test_tool"}}
	err := client.HealthCheck(context.Background())
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestVSphereExecutor_HealthCheck_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	exec := NewVSphereExecutor(srv.URL, "admin", "password", true, testLogger())
	err := exec.HealthCheck(context.Background())
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestVSphereExecutor_HealthCheck_ConnectionError(t *testing.T) {
	exec := NewVSphereExecutor("http://127.0.0.1:1", "admin", "password", true, testLogger())
	err := exec.HealthCheck(context.Background())
	if err == nil {
		t.Error("expected error for connection failure")
	}
	if !strings.Contains(err.Error(), "connection failed") {
		t.Errorf("expected 'connection failed' in error, got: %v", err)
	}
}
