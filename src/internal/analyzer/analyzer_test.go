package analyzer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

// mockLLM implements domain.LLMProvider for testing.
type mockLLM struct {
	responses []*domain.ChatResponse
	errors    []error
	callIdx   int
	requests  []*domain.ChatRequest
}

func (m *mockLLM) Chat(_ context.Context, req *domain.ChatRequest) (*domain.ChatResponse, error) {
	idx := m.callIdx
	m.callIdx++
	m.requests = append(m.requests, req)

	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
	return &domain.ChatResponse{Content: "fallback", Done: true}, nil
}

// mockTools implements domain.ToolExecutor for testing.
type mockTools struct {
	tools      []domain.Tool
	listErr    error
	results    map[string]*domain.ToolResult
	executeErr map[string]error
	calls      []domain.ToolCall
}

func (m *mockTools) ListTools(_ context.Context) ([]domain.Tool, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.tools, nil
}

func (m *mockTools) Execute(_ context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	m.calls = append(m.calls, call)
	if err, ok := m.executeErr[call.Name]; ok && err != nil {
		return nil, err
	}
	if result, ok := m.results[call.Name]; ok {
		return result, nil
	}
	return &domain.ToolResult{CallID: call.ID, Content: "ok"}, nil
}

func newTestAlert() *domain.Alert {
	return &domain.Alert{
		ID:          "test-1",
		Fingerprint: "fp-123",
		Source:      "alertmanager",
		Severity:    domain.SeverityCritical,
		Name:        "HighCPU",
		RawText:     "CPU usage is at 95% on host-1",
	}
}

func TestAnalyze_SimpleNoToolCalls(t *testing.T) {
	llm := &mockLLM{
		responses: []*domain.ChatResponse{
			{Content: "CPU is high, restart the service.", Done: true},
		},
	}
	tools := &mockTools{
		tools: []domain.Tool{
			{Name: "check_cpu", Description: "Check CPU usage"},
		},
	}

	a := New(llm, tools, "", "en", 10, nil)
	result, err := a.Analyze(context.Background(), newTestAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text != "CPU is high, restart the service." {
		t.Errorf("unexpected text: %q", result.Text)
	}
	if result.AlertFingerprint != "fp-123" {
		t.Errorf("unexpected fingerprint: %q", result.AlertFingerprint)
	}
	if len(result.ToolsUsed) != 0 {
		t.Errorf("expected no tools used, got %v", result.ToolsUsed)
	}
}

func TestAnalyze_ToolCallingLoop(t *testing.T) {
	llm := &mockLLM{
		responses: []*domain.ChatResponse{
			{
				Content: "",
				ToolCalls: []domain.ToolCall{
					{ID: "tc-1", Name: "check_cpu", Input: map[string]interface{}{"host": "host-1"}},
				},
				Done: false,
			},
			{
				Content: "CPU is at 95%, recommend scaling.",
				Done:    true,
			},
		},
	}
	tools := &mockTools{
		tools: []domain.Tool{
			{Name: "check_cpu", Description: "Check CPU usage"},
		},
		results: map[string]*domain.ToolResult{
			"check_cpu": {CallID: "tc-1", Content: "CPU: 95%", IsError: false},
		},
	}

	a := New(llm, tools, "", "en", 10, nil)
	result, err := a.Analyze(context.Background(), newTestAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text != "CPU is at 95%, recommend scaling." {
		t.Errorf("unexpected text: %q", result.Text)
	}
	if len(result.ToolsUsed) != 1 || result.ToolsUsed[0] != "check_cpu" {
		t.Errorf("unexpected tools used: %v", result.ToolsUsed)
	}
	if len(tools.calls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(tools.calls))
	}
}

func TestAnalyze_MaxIterationsReached(t *testing.T) {
	// LLM always returns tool calls, never finishes.
	llm := &mockLLM{
		responses: []*domain.ChatResponse{
			{ToolCalls: []domain.ToolCall{{ID: "tc-1", Name: "check_cpu"}}, Done: false},
			{ToolCalls: []domain.ToolCall{{ID: "tc-2", Name: "check_cpu"}}, Done: false},
			{ToolCalls: []domain.ToolCall{{ID: "tc-3", Name: "check_cpu"}}, Done: false},
		},
	}
	tools := &mockTools{
		tools: []domain.Tool{{Name: "check_cpu", Description: "Check CPU"}},
		results: map[string]*domain.ToolResult{
			"check_cpu": {CallID: "tc-1", Content: "95%"},
		},
	}

	a := New(llm, tools, "", "en", 2, nil)
	result, err := a.Analyze(context.Background(), newTestAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text != "Analysis incomplete: maximum iterations reached" {
		t.Errorf("unexpected text: %q", result.Text)
	}
}

func TestAnalyze_LLMError(t *testing.T) {
	llm := &mockLLM{
		errors: []error{errors.New("LLM unavailable")},
	}
	tools := &mockTools{
		tools: []domain.Tool{{Name: "check_cpu", Description: "Check CPU"}},
	}

	a := New(llm, tools, "", "en", 10, nil)
	_, err := a.Analyze(context.Background(), newTestAlert())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errors.Unwrap(err)) && err.Error() == "" {
		t.Errorf("expected non-empty error message")
	}
}

func TestAnalyze_ToolExecutionError(t *testing.T) {
	llm := &mockLLM{
		responses: []*domain.ChatResponse{
			{
				ToolCalls: []domain.ToolCall{
					{ID: "tc-1", Name: "check_cpu"},
				},
				Done: false,
			},
			{
				Content: "Tool failed but here is my analysis.",
				Done:    true,
			},
		},
	}
	tools := &mockTools{
		tools:      []domain.Tool{{Name: "check_cpu", Description: "Check CPU"}},
		executeErr: map[string]error{"check_cpu": errors.New("connection refused")},
	}

	a := New(llm, tools, "", "en", 10, nil)
	result, err := a.Analyze(context.Background(), newTestAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text != "Tool failed but here is my analysis." {
		t.Errorf("unexpected text: %q", result.Text)
	}
	if len(result.ToolsUsed) != 1 || result.ToolsUsed[0] != "check_cpu" {
		t.Errorf("unexpected tools used: %v", result.ToolsUsed)
	}

	// Verify the error was passed back to the LLM.
	if len(llm.requests) < 2 {
		t.Fatal("expected at least 2 LLM calls")
	}
	lastReq := llm.requests[1]
	found := false
	for _, msg := range lastReq.Messages {
		if msg.Role == "tool" && msg.ToolResult != nil && msg.ToolResult.IsError {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected tool error result in messages")
	}
}

func TestAnalyze_ListToolsError(t *testing.T) {
	llm := &mockLLM{}
	tools := &mockTools{
		listErr: errors.New("cannot list tools"),
	}

	a := New(llm, tools, "", "en", 10, nil)
	_, err := a.Analyze(context.Background(), newTestAlert())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestAnalyze_RussianPrompt(t *testing.T) {
	llm := &mockLLM{
		responses: []*domain.ChatResponse{
			{Content: "Diagnosis in Russian.", Done: true},
		},
	}
	tools := &mockTools{}

	a := New(llm, tools, "", "ru", 10, nil)
	_, err := a.Analyze(context.Background(), newTestAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(llm.requests) != 1 {
		t.Fatal("expected 1 LLM request")
	}
	if !strings.Contains(llm.requests[0].SystemPrompt, "ТОЛЬКО русский") {
		t.Error("expected Russian system prompt")
	}
}

func TestAnalyze_CustomSystemPrompt(t *testing.T) {
	llm := &mockLLM{
		responses: []*domain.ChatResponse{
			{Content: "Custom analysis.", Done: true},
		},
	}
	tools := &mockTools{}

	custom := "You are a custom assistant."
	a := New(llm, tools, custom, "en", 10, nil)
	_, err := a.Analyze(context.Background(), newTestAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if llm.requests[0].SystemPrompt != custom {
		t.Errorf("expected custom system prompt, got %q", llm.requests[0].SystemPrompt)
	}
}

func TestAnalyze_UserCommand(t *testing.T) {
	llm := &mockLLM{
		responses: []*domain.ChatResponse{
			{Content: "Analysis.", Done: true},
		},
	}
	tools := &mockTools{}

	alert := newTestAlert()
	alert.UserCommand = "check memory too"

	a := New(llm, tools, "", "en", 10, nil)
	_, err := a.Analyze(context.Background(), alert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	userMsg := llm.requests[0].Messages[0]
	if userMsg.Role != "user" {
		t.Errorf("expected user role, got %q", userMsg.Role)
	}
	if !strings.Contains(userMsg.Content, "CPU usage is at 95% on host-1") {
		t.Errorf("expected alert text in user content, got %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, "check memory too") {
		t.Errorf("expected user command in user content, got %q", userMsg.Content)
	}
}

// --- Context compaction / tool-output cap ---

func TestCapToolContent(t *testing.T) {
	full := &domain.ToolResult{CallID: "c1", Content: strings.Repeat("x", 500)}

	// max=0 → passthrough
	if got := capToolContent(full, 0); got != full {
		t.Errorf("max=0 should passthrough")
	}
	// content under cap → passthrough
	if got := capToolContent(full, 1000); got != full {
		t.Errorf("under cap should passthrough")
	}
	// over cap → truncated with marker
	got := capToolContent(full, 100)
	if got == full {
		t.Fatal("over cap should return new ToolResult")
	}
	if !strings.Contains(got.Content, "truncated 400 of 500 chars") {
		t.Errorf("expected truncation marker, got %q", got.Content)
	}
	if got.CallID != "c1" {
		t.Errorf("CallID not preserved")
	}
	// nil → nil
	if got := capToolContent(nil, 100); got != nil {
		t.Errorf("nil input should return nil")
	}
}

func TestCompactToolHistory(t *testing.T) {
	msg := func(i int, content string) domain.Message {
		return domain.Message{
			Role: "tool",
			ToolResult: &domain.ToolResult{
				CallID:  fmt.Sprintf("c%d", i),
				Content: content,
			},
		}
	}
	bigPayload := strings.Repeat("a", 500)

	messages := []domain.Message{
		{Role: "user", Content: "analyze"},
		msg(1, bigPayload),
		{Role: "assistant", Content: "thinking"},
		msg(2, bigPayload),
		msg(3, bigPayload),
		msg(4, bigPayload),
		msg(5, bigPayload),
	}

	// keep=3 → messages 1 and 2 should be compacted, 3/4/5 stay full.
	n := compactToolHistory(messages, 3)
	if n != 2 {
		t.Errorf("expected 2 compactions, got %d", n)
	}

	// message[1] and message[3] (= msg 1 and msg 2) should now be compacted
	if !strings.HasPrefix(messages[1].ToolResult.Content, compactedMarker) {
		t.Errorf("oldest tool result not compacted")
	}
	if !strings.HasPrefix(messages[3].ToolResult.Content, compactedMarker) {
		t.Errorf("second-oldest tool result not compacted")
	}
	if strings.HasPrefix(messages[4].ToolResult.Content, compactedMarker) {
		t.Errorf("recent tool result should NOT be compacted")
	}
	// CallID preserved on compacted entries
	if messages[1].ToolResult.CallID != "c1" {
		t.Errorf("CallID lost on compaction")
	}

	// Running again must be a no-op (already-compacted skipped).
	n = compactToolHistory(messages, 3)
	if n != 0 {
		t.Errorf("second pass should compact nothing; got %d", n)
	}

	// keep=0 → no-op
	messages2 := []domain.Message{msg(1, bigPayload), msg(2, bigPayload)}
	if compactToolHistory(messages2, 0) != 0 {
		t.Errorf("keep=0 should be no-op")
	}
	if messages2[0].ToolResult.Content != bigPayload {
		t.Errorf("keep=0 must not alter content")
	}
}
