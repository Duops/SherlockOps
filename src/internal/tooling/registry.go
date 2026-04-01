package tooling

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// Registry aggregates multiple ToolExecutor sources and dispatches
// tool calls to the executor that owns the requested tool.
type Registry struct {
	executors    []domain.ToolExecutor
	displayNames map[string]string // tool prefix → config name (e.g., "prometheus" → "victoriametrics")
	logger       *slog.Logger
}

// NewRegistry creates a new tool registry.
func NewRegistry(logger *slog.Logger) *Registry {
	return &Registry{
		executors:    make([]domain.ToolExecutor, 0),
		displayNames: make(map[string]string),
		logger:       logger,
	}
}

// Register adds a ToolExecutor to the registry.
func (r *Registry) Register(executor domain.ToolExecutor) {
	r.executors = append(r.executors, executor)
}

// RegisterNamed adds a ToolExecutor with a display name from config.
// The display name is mapped from the tool prefix to the config key.
func (r *Registry) RegisterNamed(executor domain.ToolExecutor, configName string) {
	r.executors = append(r.executors, executor)
	// Discover tool prefix from first tool.
	ctx := context.Background()
	tools, err := executor.ListTools(ctx)
	if err == nil && len(tools) > 0 {
		prefix := tools[0].Name
		for i, c := range prefix {
			if c == '_' {
				prefix = prefix[:i]
				break
			}
		}
		r.displayNames[prefix] = configName
	}
}

// DisplayName returns the config name for a tool prefix, or the prefix itself.
func (r *Registry) DisplayName(prefix string) string {
	if name, ok := r.displayNames[prefix]; ok {
		return name
	}
	return prefix
}

// ListTools aggregates tools from all registered executors.
func (r *Registry) ListTools(ctx context.Context) ([]domain.Tool, error) {
	var all []domain.Tool
	for _, exec := range r.executors {
		tools, err := exec.ListTools(ctx)
		if err != nil {
			r.logger.Warn("failed to list tools from executor", "error", err)
			continue
		}
		all = append(all, tools...)
	}
	return all, nil
}

// Execute finds the executor that owns the tool by name and delegates the call.
func (r *Registry) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	for _, exec := range r.executors {
		tools, err := exec.ListTools(ctx)
		if err != nil {
			continue
		}
		for _, t := range tools {
			if t.Name == call.Name {
				r.logger.Debug("executing tool", "tool", call.Name)
				return exec.Execute(ctx, call)
			}
		}
	}

	r.logger.Warn("tool not found", "tool", call.Name)
	return &domain.ToolResult{
		CallID:  call.ID,
		Content: fmt.Sprintf("tool %q not found", call.Name),
		IsError: true,
	}, nil
}
