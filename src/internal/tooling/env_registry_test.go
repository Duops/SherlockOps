package tooling

import (
	"context"
	"log/slog"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

// stubExecutor is a minimal ToolExecutor for testing environment registry routing.
type stubExecutor struct {
	name string
}

func (s *stubExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	return []domain.Tool{{Name: s.name, Description: "stub"}}, nil
}

func (s *stubExecutor) Execute(_ context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	return &domain.ToolResult{CallID: call.ID, Content: s.name + " executed"}, nil
}

func TestEnvRegistry_GetRegistry_ExistingEnv(t *testing.T) {
	logger := slog.Default()
	er := NewEnvRegistry(logger)

	prodReg := NewRegistry(logger)
	prodReg.Register(&stubExecutor{name: "prod_prom"})
	er.SetRegistry("prod", prodReg)

	defaultReg := NewRegistry(logger)
	defaultReg.Register(&stubExecutor{name: "default_prom"})
	er.SetRegistry("default", defaultReg)

	got := er.GetRegistry("prod")
	tools, err := got.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "prod_prom" {
		t.Errorf("expected prod_prom tool, got %v", tools)
	}
}

func TestEnvRegistry_GetRegistry_FallsBackToDefault(t *testing.T) {
	logger := slog.Default()
	er := NewEnvRegistry(logger)

	defaultReg := NewRegistry(logger)
	defaultReg.Register(&stubExecutor{name: "default_prom"})
	er.SetRegistry("default", defaultReg)

	got := er.GetRegistry("staging")
	tools, err := got.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "default_prom" {
		t.Errorf("expected default_prom tool, got %v", tools)
	}
}

func TestEnvRegistry_GetRegistry_EmptyEnvReturnsDefault(t *testing.T) {
	logger := slog.Default()
	er := NewEnvRegistry(logger)

	defaultReg := NewRegistry(logger)
	defaultReg.Register(&stubExecutor{name: "default_tool"})
	er.SetRegistry("default", defaultReg)

	got := er.GetRegistry("")
	tools, err := got.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "default_tool" {
		t.Errorf("expected default_tool, got %v", tools)
	}
}

func TestEnvRegistry_GetRegistry_NoDefaultReturnsEmptyRegistry(t *testing.T) {
	logger := slog.Default()
	er := NewEnvRegistry(logger)

	got := er.GetRegistry("nonexistent")
	tools, err := got.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected empty tool list, got %v", tools)
	}
}
