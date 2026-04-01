package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// OpenAIProvider implements domain.LLMProvider using the OpenAI Chat Completions API.
// It works with any OpenAI-compatible endpoint (OpenAI, Ollama, vLLM, Azure, etc.).
type OpenAIProvider struct {
	apiKey    string
	baseURL   string
	model     string
	maxTokens int
	client    *http.Client
}

type openaiRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []openaiMessage `json:"messages"`
	Tools     []openaiTool    `json:"tools,omitempty"`
}

type openaiMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openaiTool struct {
	Type     string         `json:"type"`
	Function openaiFunction `json:"function"`
}

type openaiFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type openaiToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openaiFunctionCall `json:"function"`
}

type openaiFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
	Error   *openaiError   `json:"error,omitempty"`
}

type openaiChoice struct {
	Message      openaiMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openaiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// Chat sends a chat request to the OpenAI-compatible API and returns the response.
func (p *OpenAIProvider) Chat(ctx context.Context, req *domain.ChatRequest) (*domain.ChatResponse, error) {
	msgs := convertToOpenAIMessages(req.SystemPrompt, req.Messages)
	tools := convertToOpenAITools(req.Tools)

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = p.maxTokens
	}

	body := openaiRequest{
		Model:     p.model,
		MaxTokens: maxTokens,
		Messages:  msgs,
		Tools:     tools,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal openai request: %w", err)
	}

	url := p.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create openai request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai API call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MB limit
	if err != nil {
		return nil, fmt.Errorf("read openai response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var apiResp openaiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal openai response: %w", err)
	}

	if apiResp.Error != nil {
		return nil, fmt.Errorf("openai API error: %s: %s", apiResp.Error.Type, apiResp.Error.Message)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("openai API returned no choices")
	}

	return parseOpenAIResponse(&apiResp.Choices[0]), nil
}

func convertToOpenAIMessages(systemPrompt string, msgs []domain.Message) []openaiMessage {
	var result []openaiMessage

	if systemPrompt != "" {
		result = append(result, openaiMessage{
			Role:    "system",
			Content: systemPrompt,
		})
	}

	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			msg := openaiMessage{
				Role:    "assistant",
				Content: m.Content,
			}
			for _, tc := range m.ToolCalls {
				inputJSON, _ := json.Marshal(tc.Input)
				msg.ToolCalls = append(msg.ToolCalls, openaiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: openaiFunctionCall{
						Name:      tc.Name,
						Arguments: string(inputJSON),
					},
				})
			}
			result = append(result, msg)
		case "tool":
			if m.ToolResult != nil {
				result = append(result, openaiMessage{
					Role:       "tool",
					Content:    m.ToolResult.Content,
					ToolCallID: m.ToolResult.CallID,
				})
			}
		default:
			result = append(result, openaiMessage{
				Role:    m.Role,
				Content: m.Content,
			})
		}
	}
	return result
}

func convertToOpenAITools(tools []domain.Tool) []openaiTool {
	if len(tools) == 0 {
		return nil
	}
	result := make([]openaiTool, len(tools))
	for i, t := range tools {
		result[i] = openaiTool{
			Type: "function",
			Function: openaiFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		}
	}
	return result
}

func parseOpenAIResponse(choice *openaiChoice) *domain.ChatResponse {
	var toolCalls []domain.ToolCall

	for _, tc := range choice.Message.ToolCalls {
		var input map[string]interface{}
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		toolCalls = append(toolCalls, domain.ToolCall{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	done := choice.FinishReason == "stop"

	return &domain.ChatResponse{
		Content:   choice.Message.Content,
		ToolCalls: toolCalls,
		Done:      done,
	}
}
