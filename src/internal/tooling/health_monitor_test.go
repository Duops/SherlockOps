package tooling

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

type targetExecutor struct {
	healthCheckExecutor
	target string
}

func (t *targetExecutor) Target() string { return t.target }

func TestCheckHealth_ReturnsResultsWithEnvAndTarget(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	reg := NewRegistry(logger)
	ok := &targetExecutor{
		healthCheckExecutor: healthCheckExecutor{tools: []domain.Tool{{Name: "prom_query"}}},
		target:              "http://vm:8481",
	}
	failed := &targetExecutor{
		healthCheckExecutor: healthCheckExecutor{tools: []domain.Tool{{Name: "loki_query"}}, checkErr: errors.New("HTTP 400")},
		target:              "http://loki:3100",
	}
	reg.RegisterNamed(ok, "victoriametrics")
	reg.Register(failed)

	results := CheckHealth(context.Background(), "pay-prod", reg, logger)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Environment != "pay-prod" || results[0].Tool != "victoriametrics" || results[0].Target != "http://vm:8481" || results[0].Status != domain.ToolHealthOK {
		t.Errorf("unexpected first result: %+v", results[0])
	}
	if results[1].Tool != "loki" || results[1].Status != domain.ToolHealthFailed || results[1].Error != "HTTP 400" {
		t.Errorf("unexpected second result: %+v", results[1])
	}
	logs := buf.String()
	if !strings.Contains(logs, "env=pay-prod") || !strings.Contains(logs, "target=http://loki:3100") {
		t.Errorf("expected env and target in logs, got: %s", logs)
	}
}

func TestHealthMonitor_SnapshotCoversAllEnvironments(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envReg := NewEnvRegistry(logger)

	defReg := NewRegistry(logger)
	defReg.Register(&healthCheckExecutor{tools: []domain.Tool{{Name: "k8s_get_pods"}}})
	envReg.SetRegistry("default", defReg)

	prodReg := NewRegistry(logger)
	prodReg.Register(&healthCheckExecutor{tools: []domain.Tool{{Name: "loki_query"}}, checkErr: errors.New("connection refused")})
	envReg.SetRegistry("prod", prodReg)

	m := NewHealthMonitor(envReg, 0, logger)
	if got := m.Snapshot(); len(got) != 0 {
		t.Fatalf("expected empty snapshot before first check, got %d", len(got))
	}

	m.CheckNow(context.Background())
	snap := m.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(snap))
	}
	// Sorted by environment then tool: "default" < "prod".
	if snap[0].Environment != "default" || snap[0].Status != domain.ToolHealthOK {
		t.Errorf("unexpected first entry: %+v", snap[0])
	}
	if snap[1].Environment != "prod" || snap[1].Status != domain.ToolHealthFailed {
		t.Errorf("unexpected second entry: %+v", snap[1])
	}
}

func TestHealthMonitor_LogsOnlyTransitions(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	envReg := NewEnvRegistry(logger)
	exec := &healthCheckExecutor{tools: []domain.Tool{{Name: "loki_query"}}, checkErr: errors.New("down")}
	reg := NewRegistry(logger)
	reg.Register(exec)
	envReg.SetRegistry("default", reg)

	m := NewHealthMonitor(envReg, 0, logger)
	m.CheckNow(context.Background())
	m.CheckNow(context.Background())
	if n := strings.Count(buf.String(), "tool health check FAILED"); n != 1 {
		t.Errorf("expected exactly 1 FAILED log for a stable failure, got %d: %s", n, buf.String())
	}

	exec.checkErr = nil
	m.CheckNow(context.Background())
	if !strings.Contains(buf.String(), "tool health check recovered") {
		t.Errorf("expected recovery log, got: %s", buf.String())
	}
}

func TestMCPClient_HealthCheckReconnects(t *testing.T) {
	var up atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		var req jsonRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{}}`, req.ID)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"k8s_pods","description":"d","inputSchema":{}}]}}`, req.ID)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	c := NewMCPClient("k8s", srv.URL+"/mcp", "", "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := c.Connect(context.Background()); err == nil {
		t.Fatal("expected initial connect to fail while server is down")
	}
	if err := c.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected health check to fail while server is down")
	}

	up.Store(true)
	if err := c.HealthCheck(context.Background()); err != nil {
		t.Fatalf("expected health check to reconnect, got %v", err)
	}
	tools, _ := c.ListTools(context.Background())
	if len(tools) != 1 {
		t.Errorf("tools after reconnect = %d, want 1", len(tools))
	}
}

func TestCheckHealth_MCPClientUsesConfiguredName(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := NewRegistry(logger)
	reg.Register(NewMCPClient("k8s-mcp", "http://127.0.0.1:1/mcp", "", "", nil, logger))

	results := CheckHealth(context.Background(), "prod", reg, logger)
	if len(results) != 1 || results[0].Tool != "k8s-mcp" || results[0].Status != domain.ToolHealthFailed {
		t.Fatalf("unexpected result: %+v", results)
	}
}
