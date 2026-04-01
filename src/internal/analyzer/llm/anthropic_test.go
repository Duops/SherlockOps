package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func TestAnthropicProvider_Chat_TextResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers.
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("unexpected api key: %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("unexpected version: %q", r.Header.Get("anthropic-version"))
		}
		if r.Header.Get("content-type") != "application/json" {
			t.Errorf("unexpected content-type: %q", r.Header.Get("content-type"))
		}

		// Verify request body.
		body, _ := io.ReadAll(r.Body)
		var req anthropicRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		if req.Model != "claude-sonnet-4-6" {
			t.Errorf("unexpected model: %q", req.Model)
		}
		if req.System != "You are helpful." {
			t.Errorf("unexpected system: %q", req.System)
		}
		if req.MaxTokens != 1024 {
			t.Errorf("unexpected max_tokens: %d", req.MaxTokens)
		}
		if len(req.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(req.Messages))
		}

		resp := anthropicResponse{
			ID: "msg-123",
			Content: []anthropicContentBlock{
				{Type: "text", Text: "Hello, world!"},
			},
			StopReason: "end_turn",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &AnthropicProvider{
		apiKey:    "test-key",
		model:     "claude-sonnet-4-6",
		maxTokens: 4096,
		client:    server.Client(),
	}
	// Override the API URL by replacing the const — we use a wrapper approach.
	// Instead, we'll use a custom transport that redirects requests.
	origURL := anthropicAPIURL
	p.client.Transport = &rewriteTransport{
		base:    http.DefaultTransport,
		origURL: origURL,
		newURL:  server.URL,
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

	if resp.Content != "Hello, world!" {
		t.Errorf("unexpected content: %q", resp.Content)
	}
	if !resp.Done {
		t.Error("expected Done=true for end_turn")
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(resp.ToolCalls))
	}
}

func TestAnthropicProvider_Chat_ToolUseResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req anthropicRequest
		json.Unmarshal(body, &req)

		if len(req.Tools) != 1 || req.Tools[0].Name != "check_cpu" {
			t.Errorf("unexpected tools: %+v", req.Tools)
		}

		resp := anthropicResponse{
			ID: "msg-456",
			Content: []anthropicContentBlock{
				{Type: "text", Text: "Let me check the CPU."},
				{
					Type:  "tool_use",
					ID:    "tu-1",
					Name:  "check_cpu",
					Input: map[string]interface{}{"host": "host-1"},
				},
			},
			StopReason: "tool_use",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &AnthropicProvider{
		apiKey:    "test-key",
		model:     "claude-sonnet-4-6",
		maxTokens: 4096,
		client:    server.Client(),
	}
	p.client.Transport = &rewriteTransport{
		base:    http.DefaultTransport,
		origURL: anthropicAPIURL,
		newURL:  server.URL,
	}

	resp, err := p.Chat(context.Background(), &domain.ChatRequest{
		SystemPrompt: "You are an SRE.",
		Messages:     []domain.Message{{Role: "user", Content: "Check CPU"}},
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

	if resp.Content != "Let me check the CPU." {
		t.Errorf("unexpected content: %q", resp.Content)
	}
	if resp.Done {
		t.Error("expected Done=false for tool_use stop reason")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "tu-1" {
		t.Errorf("unexpected tool call ID: %q", tc.ID)
	}
	if tc.Name != "check_cpu" {
		t.Errorf("unexpected tool call name: %q", tc.Name)
	}
	if tc.Input["host"] != "host-1" {
		t.Errorf("unexpected tool call input: %v", tc.Input)
	}
}

func TestAnthropicProvider_Chat_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"type":"server_error","message":"Internal server error"}}`))
	}))
	defer server.Close()

	p := &AnthropicProvider{
		apiKey:    "test-key",
		model:     "claude-sonnet-4-6",
		maxTokens: 4096,
		client:    server.Client(),
	}
	p.client.Transport = &rewriteTransport{
		base:    http.DefaultTransport,
		origURL: anthropicAPIURL,
		newURL:  server.URL,
	}

	_, err := p.Chat(context.Background(), &domain.ChatRequest{
		Messages: []domain.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestAnthropicProvider_ToolResultSerialization(t *testing.T) {
	var capturedReq anthropicRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		resp := anthropicResponse{
			ID:         "msg-789",
			Content:    []anthropicContentBlock{{Type: "text", Text: "Done."}},
			StopReason: "end_turn",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &AnthropicProvider{
		apiKey:    "test-key",
		model:     "claude-sonnet-4-6",
		maxTokens: 4096,
		client:    server.Client(),
	}
	p.client.Transport = &rewriteTransport{
		base:    http.DefaultTransport,
		origURL: anthropicAPIURL,
		newURL:  server.URL,
	}

	_, err := p.Chat(context.Background(), &domain.ChatRequest{
		SystemPrompt: "test",
		Messages: []domain.Message{
			{Role: "user", Content: "check cpu"},
			{
				Role: "assistant",
				ToolCalls: []domain.ToolCall{
					{ID: "tu-1", Name: "check_cpu", Input: map[string]interface{}{"host": "h1"}},
				},
			},
			{
				Role: "tool",
				ToolResult: &domain.ToolResult{
					CallID:  "tu-1",
					Content: "CPU: 95%",
					IsError: false,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The tool result should be converted to a user message with tool_result block.
	if len(capturedReq.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(capturedReq.Messages))
	}
	// Third message (tool result) should be role "user" in Anthropic format.
	if capturedReq.Messages[2].Role != "user" {
		t.Errorf("expected role 'user' for tool result, got %q", capturedReq.Messages[2].Role)
	}
}

// rewriteTransport redirects requests from origURL to newURL for testing.
type rewriteTransport struct {
	base    http.RoundTripper
	origURL string
	newURL  string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.String() == t.origURL {
		req.URL, _ = req.URL.Parse(t.newURL)
	}
	return t.base.RoundTrip(req)
}
