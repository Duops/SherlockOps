package domain

import (
	"context"
	"time"
)

// RequestIDFromContext extracts the request ID from a context.
// This is a convenience re-export; the middleware package stores the value.
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

type contextKeyType string

const requestIDKey contextKeyType = "request_id"

// ContextWithRequestID returns a new context with the given request ID.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// Receiver parses incoming webhook payloads into normalized alerts.
type Receiver interface {
	// Source returns the receiver name (e.g., "alertmanager").
	Source() string
	// Parse converts a raw HTTP body into one or more alerts.
	Parse(ctx context.Context, body []byte, headers map[string]string) ([]Alert, error)
}

// Cache stores and retrieves alert analysis results.
type Cache interface {
	// Get returns a cached analysis or nil if not found / expired.
	Get(ctx context.Context, fingerprint string) (*AnalysisResult, error)
	// Set stores an analysis result.
	Set(ctx context.Context, result *AnalysisResult) error
	// MarkResolved sets the resolved timestamp for an alert.
	MarkResolved(ctx context.Context, fingerprint string, resolvedAt time.Time) error
	// List returns recent entries ordered by created_at DESC with total count.
	List(ctx context.Context, limit int, offset int) ([]*AnalysisResult, int, error)
	// Stats returns aggregate cache statistics.
	Stats(ctx context.Context) (*CacheStats, error)
	// Close releases resources.
	Close() error
}

// CacheStats holds aggregate statistics about the cache.
type CacheStats struct {
	TotalCount    int     `json:"total_count"`
	ResolvedCount int     `json:"resolved_count"`
	AvgTextLength float64 `json:"avg_text_length"`
}

// RunbookMatcher finds runbooks relevant to a given alert and formats them for
// injection into the LLM prompt.
type RunbookMatcher interface {
	// MatchAlert returns whether any runbooks matched and a pre-formatted
	// context block ready for prompt injection.
	MatchAlert(alert *Alert) (hasMatch bool, contextBlock string)
}

// Analyzer runs LLM-based alert analysis with tool calling.
type Analyzer interface {
	// Analyze processes an alert and returns the analysis text.
	Analyze(ctx context.Context, alert *Alert) (*AnalysisResult, error)
}

// LLMProvider is the interface for LLM API communication.
type LLMProvider interface {
	// Chat sends messages to the LLM and returns the response.
	// It handles the tool calling loop internally.
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}

// ChatRequest represents a request to the LLM.
type ChatRequest struct {
	SystemPrompt string
	Messages     []Message
	Tools        []Tool
	MaxTokens    int
}

// Message is a single message in the conversation.
type Message struct {
	Role       string       `json:"role"` // "user", "assistant", "tool"
	Content    string       `json:"content,omitempty"`
	ToolCalls  []ToolCall   `json:"tool_calls,omitempty"`
	ToolResult *ToolResult  `json:"tool_result,omitempty"`
}

// ChatResponse is the LLM's reply.
type ChatResponse struct {
	Content   string     // final text content
	ToolCalls []ToolCall // tool calls to execute (empty if done)
	Done      bool       // true if this is the final response
}

// ToolExecutor executes tool calls.
type ToolExecutor interface {
	// ListTools returns all available tools.
	ListTools(ctx context.Context) ([]Tool, error)
	// Execute runs a tool call and returns the result.
	Execute(ctx context.Context, call ToolCall) (*ToolResult, error)
}

// Messenger sends analysis results to messaging platforms.
type Messenger interface {
	// Name returns the messenger name (e.g., "slack").
	Name() string
	// Start begins listening for messages (listener mode).
	Start(ctx context.Context, handler func(alert *Alert)) error

	// SendAlert posts the raw alert to a channel and returns a MessageRef
	// for later reply/edit (Phase 1 of two-phase delivery).
	SendAlert(ctx context.Context, alert *Alert) (*MessageRef, error)

	// SendAnalysisReply posts analysis as a reply (Slack thread) or edits
	// the original message (Telegram/Teams) — Phase 2 of two-phase delivery.
	SendAnalysisReply(ctx context.Context, ref *MessageRef, result *AnalysisResult) error

	// SendAnalysis posts alert+analysis together (used in bot listener mode
	// where two-phase delivery is not needed).
	SendAnalysis(ctx context.Context, alert *Alert, result *AnalysisResult) error
	// SendError posts an error message.
	SendError(ctx context.Context, alert *Alert, err error) error
	// Stop gracefully shuts down the messenger.
	Stop(ctx context.Context) error
}
