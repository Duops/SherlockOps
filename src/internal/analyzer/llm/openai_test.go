package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

func TestOpenAIProvider_Chat_TextResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers.
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content-type: %q", r.Header.Get("Content-Type"))
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %q", r.URL.Path)
		}

		body, _ := io.ReadAll(r.Body)
		var req openaiRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		if req.Model != "gpt-4o" {
			t.Errorf("unexpected model: %q", req.Model)
		}
		if req.MaxTokens != 1024 {
			t.Errorf("unexpected max_tokens: %d", req.MaxTokens)
		}
		// System prompt should be the first message.
		if len(req.Messages) < 2 {
			t.Fatalf("expected at least 2 messages, got %d", len(req.Messages))
		}
		if req.Messages[0].Role != "system" {
			t.Errorf("expected system message first, got %q", req.Messages[0].Role)
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{
					Message:      openaiMessage{Role: "assistant", Content: "Hello from OpenAI!"},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &OpenAIProvider{
		apiKey:    "test-key",
		baseURL:   server.URL,
		model:     "gpt-4o",
		maxTokens: 4096,
		client:    server.Client(),
	}

	resp, err := p.Chat(context.Background(), &domain.ChatRequest{
		SystemPrompt: "You are helpful.",
		Messages: []domain.Message{
			{Role: "user", Content: "Say hello"},
		},
		MaxTokens: 1024,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Content != "Hello from OpenAI!" {
		t.Errorf("unexpected content: %q", resp.Content)
	}
	if !resp.Done {
		t.Error("expected Done=true for stop")
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(resp.ToolCalls))
	}
}

func TestOpenAIProvider_Chat_ToolCallResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req openaiRequest
		json.Unmarshal(body, &req)

		if len(req.Tools) != 1 || req.Tools[0].Function.Name != "check_cpu" {
			t.Errorf("unexpected tools: %+v", req.Tools)
		}
		if req.Tools[0].Type != "function" {
			t.Errorf("unexpected tool type: %q", req.Tools[0].Type)
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{
					Message: openaiMessage{
						Role:    "assistant",
						Content: "Let me check.",
						ToolCalls: []openaiToolCall{
							{
								ID:   "call-1",
								Type: "function",
								Function: openaiFunctionCall{
									Name:      "check_cpu",
									Arguments: `{"host":"host-1"}`,
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &OpenAIProvider{
		apiKey:    "test-key",
		baseURL:   server.URL,
		model:     "gpt-4o",
		maxTokens: 4096,
		client:    server.Client(),
	}

	resp, err := p.Chat(context.Background(), &domain.ChatRequest{
		Messages: []domain.Message{{Role: "user", Content: "Check CPU"}},
		Tools: []domain.Tool{
			{
				Name:        "check_cpu",
				Description: "Check CPU usage",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"host": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Content != "Let me check." {
		t.Errorf("unexpected content: %q", resp.Content)
	}
	if resp.Done {
		t.Error("expected Done=false for tool_calls")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call-1" {
		t.Errorf("unexpected ID: %q", tc.ID)
	}
	if tc.Name != "check_cpu" {
		t.Errorf("unexpected name: %q", tc.Name)
	}
	if tc.Input["host"] != "host-1" {
		t.Errorf("unexpected input: %v", tc.Input)
	}
}

func TestOpenAIProvider_Chat_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"type":"server_error","message":"Internal error"}}`))
	}))
	defer server.Close()

	p := &OpenAIProvider{
		apiKey:    "test-key",
		baseURL:   server.URL,
		model:     "gpt-4o",
		maxTokens: 4096,
		client:    server.Client(),
	}

	_, err := p.Chat(context.Background(), &domain.ChatRequest{
		Messages: []domain.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestOpenAIProvider_Chat_NoChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{Choices: []openaiChoice{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &OpenAIProvider{
		apiKey:    "test-key",
		baseURL:   server.URL,
		model:     "gpt-4o",
		maxTokens: 4096,
		client:    server.Client(),
	}

	_, err := p.Chat(context.Background(), &domain.ChatRequest{
		Messages: []domain.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestOpenAIProvider_ToolResultSerialization(t *testing.T) {
	var capturedReq openaiRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		resp := openaiResponse{
			Choices: []openaiChoice{
				{
					Message:      openaiMessage{Role: "assistant", Content: "Done."},
					FinishReason: "stop",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &OpenAIProvider{
		apiKey:    "test-key",
		baseURL:   server.URL,
		model:     "gpt-4o",
		maxTokens: 4096,
		client:    server.Client(),
	}

	_, err := p.Chat(context.Background(), &domain.ChatRequest{
		SystemPrompt: "test",
		Messages: []domain.Message{
			{Role: "user", Content: "check cpu"},
			{
				Role: "assistant",
				ToolCalls: []domain.ToolCall{
					{ID: "call-1", Name: "check_cpu", Input: map[string]interface{}{"host": "h1"}},
				},
			},
			{
				Role: "tool",
				ToolResult: &domain.ToolResult{
					CallID:  "call-1",
					Content: "CPU: 95%",
					IsError: false,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// System + user + assistant + tool = 4 messages.
	if len(capturedReq.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(capturedReq.Messages))
	}
	// Tool result message.
	toolMsg := capturedReq.Messages[3]
	if toolMsg.Role != "tool" {
		t.Errorf("expected role 'tool', got %q", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call-1" {
		t.Errorf("expected tool_call_id 'call-1', got %q", toolMsg.ToolCallID)
	}
	if toolMsg.Content != "CPU: 95%" {
		t.Errorf("unexpected content: %q", toolMsg.Content)
	}
}

func TestNewProvider_Factory(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		wantErr  bool
	}{
		{"claude", "claude", false},
		{"openai", "openai", false},
		{"openai-compatible", "openai-compatible", false},
		{"unknown", "gemini", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewProvider(tt.provider, "key", "", "model", 1024)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p == nil {
				t.Error("expected non-nil provider")
			}
		})
	}
}
