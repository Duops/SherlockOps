package tooling

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// jsonRPCRequest is a JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      int         `json:"id,omitempty"` // 0 = notification (no response expected)
}

// jsonRPCResponse is a JSON-RPC 2.0 response.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
	ID      int             `json:"id"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// toolsListResult is the result of tools/list.
type toolsListResult struct {
	Tools []mcpTool `json:"tools"`
}

type mcpTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// toolsCallParams is the params for tools/call.
type toolsCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// toolsCallResult is the result of tools/call.
type toolsCallResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// MCPClient communicates with an MCP server over Streamable HTTP transport.
// Implements the MCP protocol: initialize → initialized notification → tools/list → tools/call.
type MCPClient struct {
	name      string
	url       string
	auth      string // "bearer", "basic", ""
	token     string
	headers   map[string]string
	client    *http.Client
	tools     []domain.Tool
	sessionID string // Mcp-Session-Id from server
	idCounter atomic.Int64
	logger    *slog.Logger
}

// NewMCPClient creates a new MCP client.
func NewMCPClient(name, url, auth, token string, headers map[string]string, logger *slog.Logger) *MCPClient {
	if headers == nil {
		headers = make(map[string]string)
	}
	return &MCPClient{
		name:    name,
		url:     url,
		auth:    auth,
		token:   token,
		headers: headers,
		client:  &http.Client{Timeout: 60 * time.Second},
		logger:  logger,
	}
}

func (c *MCPClient) nextID() int {
	return int(c.idCounter.Add(1))
}

// Connect performs MCP handshake and discovers available tools.
// Tries configured URL first, then /mcp path.
func (c *MCPClient) Connect(ctx context.Context) error {
	urls := []string{c.url}
	base := strings.TrimRight(c.url, "/")
	if !strings.HasSuffix(base, "/mcp") {
		urls = append(urls, base+"/mcp")
	}

	var lastErr error
	for _, u := range urls {
		c.logger.Debug("trying MCP endpoint", "name", c.name, "url", u)
		c.url = u
		err := c.handshake(ctx)
		if err != nil {
			lastErr = err
			c.logger.Debug("endpoint failed", "url", u, "error", err)
			continue
		}
		return nil
	}

	return fmt.Errorf("mcp connect %s: all endpoints failed, last error: %w", c.name, lastErr)
}

// handshake performs initialize → initialized → tools/list.
func (c *MCPClient) handshake(ctx context.Context) error {
	// Step 1: initialize
	initParams := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "sherlockops",
			"version": "1.0.0",
		},
	}
	resp, err := c.rpcCall(ctx, "initialize", initParams)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	c.logger.Debug("mcp initialized", "name", c.name, "result", string(resp.Result))

	// Step 2: send initialized notification (no ID = notification)
	if err := c.rpcNotify(ctx, "notifications/initialized", nil); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}

	// Step 3: tools/list
	toolsResp, err := c.rpcCall(ctx, "tools/list", nil)
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}

	var result toolsListResult
	if err := json.Unmarshal(toolsResp.Result, &result); err != nil {
		return fmt.Errorf("parse tools/list: %w", err)
	}

	c.tools = make([]domain.Tool, 0, len(result.Tools))
	for _, t := range result.Tools {
		c.tools = append(c.tools, domain.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	c.logger.Info("mcp client connected", "name", c.name, "url", c.url, "tools", len(c.tools))
	return nil
}

// ListTools returns the cached tools discovered via Connect.
func (c *MCPClient) ListTools(_ context.Context) ([]domain.Tool, error) {
	return c.tools, nil
}

// Execute sends a tools/call JSON-RPC request and returns the result.
func (c *MCPClient) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	params := toolsCallParams{
		Name:      call.Name,
		Arguments: call.Input,
	}

	resp, err := c.rpcCall(ctx, "tools/call", params)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("mcp call error: %v", err),
			IsError: true,
		}, nil
	}

	var result toolsCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("mcp parse error: %v", err),
			IsError: true,
		}, nil
	}

	var text string
	for _, cont := range result.Content {
		if cont.Type == "text" {
			text += cont.Text
		}
	}

	return &domain.ToolResult{
		CallID:  call.ID,
		Content: text,
		IsError: result.IsError,
	}, nil
}

// rpcCall sends a JSON-RPC request and waits for a response.
func (c *MCPClient) rpcCall(ctx context.Context, method string, params interface{}) (*jsonRPCResponse, error) {
	id := c.nextID()
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      id,
	}

	respBody, respHeaders, err := c.doPost(ctx, req)
	if err != nil {
		return nil, err
	}

	// Capture session ID from response headers.
	if sid := respHeaders.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	// Parse response — may be plain JSON or SSE.
	return c.parseResponse(respBody, respHeaders.Get("Content-Type"))
}

// rpcNotify sends a JSON-RPC notification (no response expected).
func (c *MCPClient) rpcNotify(ctx context.Context, method string, params interface{}) error {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		// ID = 0 means notification
	}

	_, _, err := c.doPost(ctx, req)
	return err
}

// doPost sends the JSON-RPC request and returns raw response body + headers.
func (c *MCPClient) doPost(ctx context.Context, req jsonRPCRequest) ([]byte, http.Header, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	c.applyAuth(httpReq)
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("http request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 10<<20)) // 10 MB limit
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}

	// Accept 200 and 202 (notifications may return 202 Accepted).
	if httpResp.StatusCode != http.StatusOK && httpResp.StatusCode != http.StatusAccepted {
		return nil, nil, fmt.Errorf("http status %d: %s", httpResp.StatusCode, string(respBody))
	}

	return respBody, httpResp.Header, nil
}

// parseResponse handles both plain JSON and SSE formatted responses.
func (c *MCPClient) parseResponse(body []byte, contentType string) (*jsonRPCResponse, error) {
	// If SSE (text/event-stream), extract JSON from "data:" lines.
	if strings.Contains(contentType, "text/event-stream") || bytes.HasPrefix(body, []byte("event:")) || bytes.HasPrefix(body, []byte("data:")) {
		return c.parseSSEResponse(body)
	}

	// Plain JSON response.
	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal json response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return &rpcResp, nil
}

// parseSSEResponse extracts the JSON-RPC response from SSE data lines.
func (c *MCPClient) parseSSEResponse(body []byte) (*jsonRPCResponse, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			jsonData := strings.TrimPrefix(line, "data: ")
			var rpcResp jsonRPCResponse
			if err := json.Unmarshal([]byte(jsonData), &rpcResp); err != nil {
				continue // skip non-JSON data lines
			}
			if rpcResp.Error != nil {
				return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
			}
			if rpcResp.Result != nil {
				return &rpcResp, nil
			}
		}
	}
	return nil, fmt.Errorf("no valid JSON-RPC response in SSE stream")
}

func (c *MCPClient) applyAuth(req *http.Request) {
	switch c.auth {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+c.token)
	case "basic":
		encoded := base64.StdEncoding.EncodeToString([]byte(c.token))
		req.Header.Set("Authorization", "Basic "+encoded)
	}
}
