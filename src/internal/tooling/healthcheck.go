package tooling

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

// HealthChecker is an optional interface that tool executors can implement
// to provide a lightweight connectivity check.
type HealthChecker interface {
	HealthCheck(ctx context.Context) error
}

// Targeter is an optional interface reporting what an executor connects to.
type Targeter interface {
	Target() string
}

// Namer is an optional interface for executors named in config (e.g. MCP clients).
type Namer interface {
	Name() string
}

// CheckHealth probes all executors in a registry, logs each outcome with the env name, and returns the results.
func CheckHealth(ctx context.Context, env string, reg *Registry, logger *slog.Logger) []domain.ToolHealth {
	results := checkRegistry(ctx, env, reg)
	for _, r := range results {
		attrs := []any{"env", r.Environment, "tool", r.Tool, "target", r.Target, "tools_count", r.ToolsCount}
		switch r.Status {
		case domain.ToolHealthFailed:
			logger.Error("tool health check FAILED", append(attrs, "error", r.Error)...)
		case domain.ToolHealthOK:
			logger.Info("tool health check OK", append(attrs, "latency_ms", r.LatencyMS)...)
		default:
			logger.Info("tool registered (no health check)", attrs...)
		}
	}
	return results
}

// checkRegistry probes every executor of one registry without logging.
func checkRegistry(ctx context.Context, env string, reg *Registry) []domain.ToolHealth {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	results := make([]domain.ToolHealth, 0, len(reg.executors))
	for _, exec := range reg.executors {
		r := domain.ToolHealth{Environment: env, Tool: "unknown", CheckedAt: time.Now().UTC()}
		if t, ok := exec.(Targeter); ok {
			r.Target = t.Target()
		}
		if n, ok := exec.(Namer); ok && n.Name() != "" {
			r.Tool = n.Name()
		}

		tools, err := exec.ListTools(ctx)
		if err != nil {
			r.Status = domain.ToolHealthFailed
			r.Error = "cannot list tools: " + err.Error()
			results = append(results, r)
			continue
		}
		r.ToolsCount = len(tools)
		if n, ok := exec.(Namer); ok && n.Name() != "" {
			r.Tool = n.Name()
		} else if len(tools) > 0 {
			r.Tool = reg.DisplayName(toolPrefix(tools[0].Name))
		}

		hc, ok := exec.(HealthChecker)
		if !ok {
			r.Status = domain.ToolHealthUnchecked
			results = append(results, r)
			continue
		}
		start := time.Now()
		err = hc.HealthCheck(ctx)
		r.LatencyMS = time.Since(start).Milliseconds()
		if err != nil {
			r.Status = domain.ToolHealthFailed
			r.Error = err.Error()
		} else {
			r.Status = domain.ToolHealthOK
		}
		results = append(results, r)
	}
	return results
}

// toolPrefix returns the executor category from a tool name ("k8s_get_pods" → "k8s").
func toolPrefix(name string) string {
	if i := strings.IndexByte(name, '_'); i > 0 {
		return name[:i]
	}
	return name
}

// HealthCheck for KubernetesExecutor — asks the API server for its version.
func (k *KubernetesExecutor) HealthCheck(ctx context.Context) error {
	if _, err := k.clientset.Discovery().ServerVersion(); err != nil {
		return fmt.Errorf("api server: %w", err)
	}
	return nil
}

// HealthCheck for PrometheusExecutor — queries "up" metric.
func (p *PrometheusExecutor) HealthCheck(ctx context.Context) error {
	u := fmt.Sprintf("%s/api/v1/query?query=1", p.url)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	if p.username != "" {
		req.SetBasicAuth(p.username, p.password)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// HealthCheck for LokiExecutor — queries labels endpoint.
func (l *LokiExecutor) HealthCheck(ctx context.Context) error {
	u := fmt.Sprintf("%s/loki/api/v1/labels", l.url)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	if l.username != "" {
		req.SetBasicAuth(l.username, l.password)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// HealthCheck for MCPClient — reconnects if no tools were discovered yet.
func (c *MCPClient) HealthCheck(ctx context.Context) error {
	tools, _ := c.ListTools(ctx)
	if len(tools) > 0 {
		return nil
	}
	if err := c.Connect(ctx); err != nil {
		return err
	}
	tools, _ = c.ListTools(ctx)
	if len(tools) == 0 {
		return fmt.Errorf("no tools discovered")
	}
	return nil
}

// HealthCheck for VSphereExecutor — tries to authenticate.
func (v *VSphereExecutor) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", v.url+"/api", nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	resp.Body.Close()
	return nil
}
