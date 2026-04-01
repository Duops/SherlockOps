package analyzer

import (
	"context"
	"log/slog"
	"testing"

	"github.com/shchepetkov/sherlockops/internal/tooling"
	"github.com/shchepetkov/sherlockops/internal/domain"
)

// envMockTools implements domain.ToolExecutor with a label to identify the environment.
type envMockTools struct {
	label string
}

func (m *envMockTools) ListTools(_ context.Context) ([]domain.Tool, error) {
	return []domain.Tool{{Name: m.label, Description: "env tool"}}, nil
}

func (m *envMockTools) Execute(_ context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	return &domain.ToolResult{CallID: call.ID, Content: m.label + " result"}, nil
}

func newEnvTestAlert(env string) *domain.Alert {
	return &domain.Alert{
		ID:          "test-env-1",
		Fingerprint: "fp-env-123",
		Source:      "alertmanager",
		Severity:    domain.SeverityWarning,
		Name:        "HighMemory",
		RawText:     "Memory usage at 90%",
		Environment: env,
	}
}

func TestEnvAnalyzer_PicksCorrectToolsByEnvironment(t *testing.T) {
	logger := slog.Default()

	// Set up per-env registries with different tooling.
	envReg := tooling.NewEnvRegistry(logger)

	defaultReg := tooling.NewRegistry(logger)
	defaultReg.Register(&envMockTools{label: "default_tool"})
	envReg.SetRegistry("default", defaultReg)

	prodReg := tooling.NewRegistry(logger)
	prodReg.Register(&envMockTools{label: "prod_tool"})
	envReg.SetRegistry("prod", prodReg)

	// LLM that returns a tool call on first iteration, then final answer.
	llm := &mockLLM{
		responses: []*domain.ChatResponse{
			{
				ToolCalls: []domain.ToolCall{
					{ID: "tc-1", Name: "prod_tool", Input: map[string]interface{}{}},
				},
				Done: false,
			},
			{
				Content: "Prod analysis done.",
				Done:    true,
			},
		},
	}

	ea := NewEnvAnalyzer(llm, envReg, "", "en", 10, logger)

	result, err := ea.Analyze(context.Background(), newEnvTestAlert("prod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text != "Prod analysis done." {
		t.Errorf("unexpected text: %q", result.Text)
	}
	if len(result.ToolsUsed) != 1 || result.ToolsUsed[0] != "prod_tool" {
		t.Errorf("expected [prod_tool], got %v", result.ToolsUsed)
	}
}

func TestEnvAnalyzer_FallsBackToDefault(t *testing.T) {
	logger := slog.Default()

	envReg := tooling.NewEnvRegistry(logger)

	defaultReg := tooling.NewRegistry(logger)
	defaultReg.Register(&envMockTools{label: "default_tool"})
	envReg.SetRegistry("default", defaultReg)

	llm := &mockLLM{
		responses: []*domain.ChatResponse{
			{Content: "Default analysis.", Done: true},
		},
	}

	ea := NewEnvAnalyzer(llm, envReg, "", "en", 10, logger)

	// Empty environment should use default.
	result, err := ea.Analyze(context.Background(), newEnvTestAlert(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text != "Default analysis." {
		t.Errorf("unexpected text: %q", result.Text)
	}
}

func TestEnvAnalyzer_UsesPerEnvSystemPrompt(t *testing.T) {
	logger := slog.Default()

	envReg := tooling.NewEnvRegistry(logger)
	defaultReg := tooling.NewRegistry(logger)
	envReg.SetRegistry("default", defaultReg)

	prodReg := tooling.NewRegistry(logger)
	envReg.SetRegistry("prod", prodReg)

	llm := &mockLLM{
		responses: []*domain.ChatResponse{
			{Content: "done", Done: true},
		},
	}

	ea := NewEnvAnalyzer(llm, envReg, "default prompt", "en", 10, logger)
	ea.SetSystemPrompt("prod", "You are analyzing production alerts.")

	_, err := ea.Analyze(context.Background(), newEnvTestAlert("prod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(llm.requests) != 1 {
		t.Fatalf("expected 1 LLM request, got %d", len(llm.requests))
	}
	if llm.requests[0].SystemPrompt != "You are analyzing production alerts." {
		t.Errorf("expected prod system prompt, got %q", llm.requests[0].SystemPrompt)
	}
}

func TestEnvAnalyzer_UnknownEnvFallsBackToDefaultPrompt(t *testing.T) {
	logger := slog.Default()

	envReg := tooling.NewEnvRegistry(logger)
	defaultReg := tooling.NewRegistry(logger)
	envReg.SetRegistry("default", defaultReg)

	llm := &mockLLM{
		responses: []*domain.ChatResponse{
			{Content: "done", Done: true},
		},
	}

	ea := NewEnvAnalyzer(llm, envReg, "default prompt", "en", 10, logger)

	_, err := ea.Analyze(context.Background(), newEnvTestAlert("staging"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if llm.requests[0].SystemPrompt != "default prompt" {
		t.Errorf("expected default prompt, got %q", llm.requests[0].SystemPrompt)
	}
}
