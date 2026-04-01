package tooling

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// mockExecutor is a test double for domain.ToolExecutor.
type mockExecutor struct {
	tools   []domain.Tool
	listErr error
	execFn  func(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error)
}

func (m *mockExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.tools, nil
}

func (m *mockExecutor) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	if m.execFn != nil {
		return m.execFn(ctx, call)
	}
	return &domain.ToolResult{CallID: call.ID, Content: "ok"}, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestRegistry_ListTools(t *testing.T) {
	logger := testLogger()
	reg := NewRegistry(logger)

	exec1 := &mockExecutor{
		tools: []domain.Tool{
			{Name: "tool_a", Description: "Tool A"},
		},
	}
	exec2 := &mockExecutor{
		tools: []domain.Tool{
			{Name: "tool_b", Description: "Tool B"},
			{Name: "tool_c", Description: "Tool C"},
		},
	}

	reg.Register(exec1)
	reg.Register(exec2)

	tools, err := reg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}
	if tools[0].Name != "tool_a" || tools[1].Name != "tool_b" || tools[2].Name != "tool_c" {
		t.Errorf("unexpected tool names: %v", tools)
	}
}

func TestRegistry_ListTools_SkipsFailedExecutors(t *testing.T) {
	logger := testLogger()
	reg := NewRegistry(logger)

	exec1 := &mockExecutor{listErr: fmt.Errorf("connection failed")}
	exec2 := &mockExecutor{
		tools: []domain.Tool{{Name: "tool_b"}},
	}

	reg.Register(exec1)
	reg.Register(exec2)

	tools, err := reg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
}

func TestRegistry_Execute_Delegates(t *testing.T) {
	logger := testLogger()
	reg := NewRegistry(logger)

	exec := &mockExecutor{
		tools: []domain.Tool{{Name: "my_tool"}},
		execFn: func(_ context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
			return &domain.ToolResult{
				CallID:  call.ID,
				Content: "executed: " + call.Name,
			}, nil
		},
	}
	reg.Register(exec)

	result, err := reg.Execute(context.Background(), domain.ToolCall{
		ID:   "call-1",
		Name: "my_tool",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Content != "executed: my_tool" {
		t.Errorf("unexpected content: %s", result.Content)
	}
	if result.IsError {
		t.Error("expected IsError=false")
	}
}

func TestRegistry_Execute_ToolNotFound(t *testing.T) {
	logger := testLogger()
	reg := NewRegistry(logger)

	exec := &mockExecutor{
		tools: []domain.Tool{{Name: "existing_tool"}},
	}
	reg.Register(exec)

	result, err := reg.Execute(context.Background(), domain.ToolCall{
		ID:   "call-1",
		Name: "missing_tool",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing tool")
	}
	if result.Content == "" {
		t.Error("expected error message in content")
	}
}

func TestRegistry_Execute_EmptyRegistry(t *testing.T) {
	logger := testLogger()
	reg := NewRegistry(logger)

	result, err := reg.Execute(context.Background(), domain.ToolCall{
		ID:   "call-1",
		Name: "any_tool",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for empty registry")
	}
}

func TestRegistry_RegisterNamed(t *testing.T) {
	logger := testLogger()
	reg := NewRegistry(logger)

	exec := &mockExecutor{
		tools: []domain.Tool{{Name: "prometheus_query", Description: "test"}},
	}
	reg.RegisterNamed(exec, "victoriametrics")

	// Verify the executor was registered and tools are listed.
	tools, err := reg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	// Verify display name was set.
	name := reg.DisplayName("prometheus")
	if name != "victoriametrics" {
		t.Errorf("expected DisplayName 'victoriametrics', got %q", name)
	}
}

func TestRegistry_RegisterNamed_NoTools(t *testing.T) {
	logger := testLogger()
	reg := NewRegistry(logger)

	exec := &mockExecutor{tools: []domain.Tool{}}
	reg.RegisterNamed(exec, "myconfig")

	// Executor should still be registered even with no tools.
	tools, err := reg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(tools))
	}
}

func TestRegistry_RegisterNamed_ListError(t *testing.T) {
	logger := testLogger()
	reg := NewRegistry(logger)

	exec := &mockExecutor{listErr: fmt.Errorf("list failed")}
	reg.RegisterNamed(exec, "myconfig")

	// Executor should still be registered even if ListTools fails during RegisterNamed.
	// The display name won't be set, but that's expected.
	name := reg.DisplayName("anything")
	if name != "anything" {
		t.Errorf("expected fallback display name 'anything', got %q", name)
	}
}

func TestRegistry_DisplayName_Fallback(t *testing.T) {
	logger := testLogger()
	reg := NewRegistry(logger)

	// When no display name is registered, the prefix itself is returned.
	name := reg.DisplayName("prometheus")
	if name != "prometheus" {
		t.Errorf("expected 'prometheus', got %q", name)
	}
}

func TestRegistry_DisplayName_WithUnderscore(t *testing.T) {
	logger := testLogger()
	reg := NewRegistry(logger)

	exec := &mockExecutor{
		tools: []domain.Tool{{Name: "loki_query", Description: "test"}},
	}
	reg.RegisterNamed(exec, "grafana-loki")

	name := reg.DisplayName("loki")
	if name != "grafana-loki" {
		t.Errorf("expected 'grafana-loki', got %q", name)
	}
}

func TestRegistry_Execute_SkipsFailedListTools(t *testing.T) {
	logger := testLogger()
	reg := NewRegistry(logger)

	// First executor has a list error; second has the tool.
	exec1 := &mockExecutor{listErr: fmt.Errorf("connection failed")}
	exec2 := &mockExecutor{
		tools: []domain.Tool{{Name: "my_tool"}},
		execFn: func(_ context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
			return &domain.ToolResult{CallID: call.ID, Content: "found it"}, nil
		},
	}
	reg.Register(exec1)
	reg.Register(exec2)

	result, err := reg.Execute(context.Background(), domain.ToolCall{
		ID:   "call-1",
		Name: "my_tool",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if result.Content != "found it" {
		t.Errorf("unexpected content: %s", result.Content)
	}
}
