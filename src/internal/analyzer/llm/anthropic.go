package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

const maxRetries = 3

const anthropicAPIURL = "https://api.anthropic.com/v1/messages"

// AnthropicProvider implements domain.LLMProvider using the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey    string
	model     string
	maxTokens int
	client    *http.Client
}

// anthropicRequest is the request body for the Anthropic Messages API.
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type anthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// anthropicResponse is the response body from the Anthropic Messages API.
type anthropicResponse struct {
	ID         string                   `json:"id"`
	Content    []anthropicContentBlock   `json:"content"`
	StopReason string                   `json:"stop_reason"`
	Usage      *anthropicUsage          `json:"usage,omitempty"`
	Error      *anthropicError          `json:"error,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicContentBlock struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input"` // Anthropic requires this field always for tool_use
}

type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Chat sends a chat request to the Anthropic API and returns the response.
func (p *AnthropicProvider) Chat(ctx context.Context, req *domain.ChatRequest) (*domain.ChatResponse, error) {
	msgs := convertToAnthropicMessages(req.Messages)
	tools := convertToAnthropicTools(req.Tools)

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = p.maxTokens
	}

	body := anthropicRequest{
		Model:     p.model,
		MaxTokens: maxTokens,
		System:    req.SystemPrompt,
		Messages:  msgs,
		Tools:     tools,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create anthropic request: %w", err)
	}

	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	var respBody []byte
	var statusCode int

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Re-create request body for retry (body was consumed).
			httpReq, _ = http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(payload))
			httpReq.Header.Set("x-api-key", p.apiKey)
			httpReq.Header.Set("anthropic-version", "2023-06-01")
			httpReq.Header.Set("content-type", "application/json")
		}

		resp, err := p.client.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("anthropic API call: %w", err)
		}

		respBody, err = io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MB limit
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read anthropic response: %w", err)
		}

		statusCode = resp.StatusCode
		if statusCode == http.StatusTooManyRequests && attempt < maxRetries {
			wait := 30 * time.Second // default wait
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				if secs, err := strconv.Atoi(retryAfter); err == nil {
					wait = time.Duration(secs) * time.Second
				}
			}
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		break
	}

	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic API error (status %d): %s", statusCode, string(respBody))
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal anthropic response: %w", err)
	}

	if apiResp.Error != nil {
		return nil, fmt.Errorf("anthropic API error: %s: %s", apiResp.Error.Type, apiResp.Error.Message)
	}

	return parseAnthropicResponse(&apiResp), nil
}

func convertToAnthropicMessages(msgs []domain.Message) []anthropicMessage {
	var result []anthropicMessage
	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			if len(m.ToolCalls) > 0 {
				var blocks []interface{}
				if m.Content != "" {
					blocks = append(blocks, map[string]interface{}{
						"type": "text",
						"text": m.Content,
					})
				}
				for _, tc := range m.ToolCalls {
					input := tc.Input
					if input == nil {
						input = map[string]interface{}{}
					}
					blocks = append(blocks, map[string]interface{}{
						"type":  "tool_use",
						"id":    tc.ID,
						"name":  tc.Name,
						"input": input,
					})
				}
				result = append(result, anthropicMessage{
					Role:    "assistant",
					Content: blocks,
				})
			} else {
				result = append(result, anthropicMessage{
					Role:    "assistant",
					Content: m.Content,
				})
			}
		case "tool":
			if m.ToolResult != nil {
				blocks := []map[string]interface{}{
					{
						"type":        "tool_result",
						"tool_use_id": m.ToolResult.CallID,
						"content":     m.ToolResult.Content,
						"is_error":    m.ToolResult.IsError,
					},
				}
				result = append(result, anthropicMessage{
					Role:    "user",
					Content: blocks,
				})
			}
		default:
			result = append(result, anthropicMessage{
				Role:    m.Role,
				Content: m.Content,
			})
		}
	}
	return result
}

func convertToAnthropicTools(tools []domain.Tool) []anthropicTool {
	if len(tools) == 0 {
		return nil
	}
	result := make([]anthropicTool, len(tools))
	for i, t := range tools {
		result[i] = anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return result
}

func parseAnthropicResponse(resp *anthropicResponse) *domain.ChatResponse {
	var text string
	var toolCalls []domain.ToolCall

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			text += block.Text
		case "tool_use":
			toolCalls = append(toolCalls, domain.ToolCall{
				ID:    block.ID,
				Name:  block.Name,
				Input: block.Input,
			})
		}
	}

	done := resp.StopReason == "end_turn"

	var total, input, output int
	if resp.Usage != nil {
		input = resp.Usage.InputTokens
		output = resp.Usage.OutputTokens
		total = input + output
	}

	return &domain.ChatResponse{
		Content:      text,
		ToolCalls:    toolCalls,
		Done:         done,
		TokensUsed:   total,
		InputTokens:  input,
		OutputTokens: output,
	}
}
