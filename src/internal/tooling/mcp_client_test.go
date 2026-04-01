package tooling

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

// newTestMCPServer creates an httptest server that implements the full MCP Streamable HTTP protocol:
// initialize → notifications/initialized → tools/list → tools/call.
func newTestMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "test-session-123")
			resp := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
					"serverInfo":      map[string]interface{}{"name": "test-mcp", "version": "1.0"},
				},
			}
			json.NewEncoder(w).Encode(resp)

		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)

		case "tools/list":
			resp := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{
							"name":        "test_tool",
							"description": "A test tool",
							"inputSchema": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"message": map[string]interface{}{"type": "string"},
								},
							},
						},
						{
							"name":        "another_tool",
							"description": "Another tool",
							"inputSchema": map[string]interface{}{},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)

		case "tools/call":
			paramsBytes, _ := json.Marshal(req.Params)
			var params toolsCallParams
			json.Unmarshal(paramsBytes, &params)

			msg := "default response"
			if v, ok := params.Arguments["message"].(string); ok {
				msg = "echo: " + v
			}

			resp := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"content": []map[string]interface{}{
						{"type": "text", "text": msg},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)

		default:
			resp := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error": map[string]interface{}{
					"code":    -32601,
					"message": "method not found",
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
}

func TestMCPClient_Connect(t *testing.T) {
	srv := newTestMCPServer(t)
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := NewMCPClient("test", srv.URL, "", "", nil, logger)

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "test_tool" {
		t.Errorf("expected test_tool, got %s", tools[0].Name)
	}
	if client.sessionID != "test-session-123" {
		t.Errorf("expected session ID 'test-session-123', got %q", client.sessionID)
	}
}

func TestMCPClient_Execute(t *testing.T) {
	srv := newTestMCPServer(t)
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := NewMCPClient("test", srv.URL, "", "", nil, logger)

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	result, err := client.Execute(context.Background(), domain.ToolCall{
		ID:    "call-1",
		Name:  "test_tool",
		Input: map[string]interface{}{"message": "hello"},
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if result.Content != "echo: hello" {
		t.Errorf("expected 'echo: hello', got %q", result.Content)
	}
	if result.CallID != "call-1" {
		t.Errorf("expected call ID 'call-1', got %q", result.CallID)
	}
}

func TestMCPClient_SSEResponse(t *testing.T) {
	// Server that returns SSE format (like kubernetes_mcp_server).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		json.NewDecoder(r.Body).Decode(&req)

		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Mcp-Session-Id", "sse-session")
			w.WriteHeader(http.StatusOK)
			resp, _ := json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo":      map[string]interface{}{"name": "sse-server"},
				},
			})
			w.Write([]byte("event: message\ndata: " + string(resp) + "\n\n"))

		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)

		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			resp, _ := json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{"name": "sse_tool", "description": "SSE tool", "inputSchema": map[string]interface{}{}},
					},
				},
			})
			w.Write([]byte("event: message\ndata: " + string(resp) + "\n\n"))
		}
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := NewMCPClient("sse-test", srv.URL, "", "", nil, logger)

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	tools, _ := client.ListTools(context.Background())
	if len(tools) != 1 || tools[0].Name != "sse_tool" {
		t.Errorf("expected sse_tool, got %v", tools)
	}
}

func TestMCPClient_BearerAuth(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		var req jsonRPCRequest
		json.NewDecoder(r.Body).Decode(&req)

		switch req.Method {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo":      map[string]interface{}{"name": "auth-test"},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req.ID,
				"result":  map[string]interface{}{"tools": []interface{}{}},
			})
		}
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := NewMCPClient("test", srv.URL, "bearer", "my-token", nil, logger)

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if receivedAuth != "Bearer my-token" {
		t.Errorf("expected 'Bearer my-token', got %q", receivedAuth)
	}
}

func TestMCPClient_RPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": 1,
			"error": map[string]interface{}{"code": -32600, "message": "invalid request"},
		})
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := NewMCPClient("test", srv.URL, "", "", nil, logger)

	err := client.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error for RPC error response")
	}
}
