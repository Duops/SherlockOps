package tooling

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

// HealthMonitor periodically probes all tool executors of all environments and
// keeps the latest results; it logs each tool once and then only status changes.
type HealthMonitor struct {
	envs     *EnvRegistry
	interval time.Duration
	logger   *slog.Logger

	mu       sync.RWMutex
	snapshot []domain.ToolHealth
	previous map[string]string // env/tool → last status
}

// NewHealthMonitor creates a monitor; interval <= 0 disables the background loop.
func NewHealthMonitor(envs *EnvRegistry, interval time.Duration, logger *slog.Logger) *HealthMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthMonitor{
		envs:     envs,
		interval: interval,
		logger:   logger,
		previous: make(map[string]string),
	}
}

// Run blocks, re-checking every interval until ctx is cancelled.
func (m *HealthMonitor) Run(ctx context.Context) {
	if m.interval <= 0 {
		return
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.CheckNow(ctx)
		}
	}
}

// CheckNow probes all environments once and updates the snapshot.
func (m *HealthMonitor) CheckNow(ctx context.Context) {
	var results []domain.ToolHealth
	for env, reg := range m.envs.Registries() {
		results = append(results, checkRegistry(ctx, env, reg)...)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Environment != results[j].Environment {
			return results[i].Environment < results[j].Environment
		}
		return results[i].Tool < results[j].Tool
	})

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range results {
		key := r.Environment + "/" + r.Tool
		prev, seen := m.previous[key]
		m.previous[key] = r.Status
		if seen && prev == r.Status {
			continue
		}
		attrs := []any{"env", r.Environment, "tool", r.Tool, "target", r.Target, "tools_count", r.ToolsCount}
		switch {
		case r.Status == domain.ToolHealthFailed:
			m.logger.Error("tool health check FAILED", append(attrs, "error", r.Error)...)
		case seen && prev == domain.ToolHealthFailed:
			m.logger.Info("tool health check recovered", append(attrs, "latency_ms", r.LatencyMS)...)
		case !seen && r.Status == domain.ToolHealthOK:
			m.logger.Info("tool health check OK", append(attrs, "latency_ms", r.LatencyMS)...)
		case !seen:
			m.logger.Info("tool registered (no health check)", attrs...)
		}
	}
	m.snapshot = results
}

// Snapshot returns a copy of the latest results.
func (m *HealthMonitor) Snapshot() []domain.ToolHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]domain.ToolHealth, len(m.snapshot))
	copy(out, m.snapshot)
	return out
}
