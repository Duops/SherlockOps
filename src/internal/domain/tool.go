package domain

import "time"

// Tool represents a tool available to the LLM agent.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// ToolCall represents a tool invocation requested by the LLM.
type ToolCall struct {
	ID    string                 `json:"id"`
	Name  string                 `json:"name"`
	Input map[string]interface{} `json:"input"`
}

// ToolResult is the outcome of executing a tool call.
type ToolResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// ToolHealth is the connectivity check result for one executor in one environment.
type ToolHealth struct {
	Environment string    `json:"environment"`
	Tool        string    `json:"tool"`
	Target      string    `json:"target,omitempty"`
	ToolsCount  int       `json:"tools_count"`
	Status      string    `json:"status"` // "ok", "failed", "unchecked"
	Error       string    `json:"error,omitempty"`
	LatencyMS   int64     `json:"latency_ms"`
	CheckedAt   time.Time `json:"checked_at"`
}

// ToolHealth status values.
const (
	ToolHealthOK        = "ok"
	ToolHealthFailed    = "failed"
	ToolHealthUnchecked = "unchecked"
)
