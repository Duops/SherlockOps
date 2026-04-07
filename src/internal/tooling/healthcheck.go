package tooling

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// HealthChecker is an optional interface that tool executors can implement
// to provide a lightweight connectivity check at startup.
type HealthChecker interface {
	HealthCheck(ctx context.Context) error
}

// CheckHealth runs health checks on all registered executors in a registry.
func CheckHealth(ctx context.Context, reg *Registry, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	for _, exec := range reg.executors {
		tools, err := exec.ListTools(ctx)
		if err != nil {
			logger.Warn("tool executor: cannot list tools", "error", err)
			continue
		}

		name := "unknown"
		if len(tools) > 0 {
			name = tools[0].Name
			for i, c := range name {
				if c == '_' {
					name = name[:i]
					break
				}
			}
		}

		if hc, ok := exec.(HealthChecker); ok {
			if err := hc.HealthCheck(ctx); err != nil {
				logger.Error("tool health check FAILED",
					"tool", name,
					"tools_count", len(tools),
					"error", err,
				)
			} else {
				logger.Info("tool health check OK",
					"tool", name,
					"tools_count", len(tools),
				)
			}
		} else {
			logger.Info("tool registered (no health check)",
				"tool", name,
				"tools_count", len(tools),
			)
		}
	}
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

// HealthCheck for MCPClient — already connected if tools are cached.
func (c *MCPClient) HealthCheck(_ context.Context) error {
	if len(c.tools) == 0 {
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
