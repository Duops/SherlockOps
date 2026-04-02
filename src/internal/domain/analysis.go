package domain

import "time"

// AnalysisResult holds the LLM analysis of an alert.
type AnalysisResult struct {
	AlertFingerprint string
	Text             string
	ToolsUsed        []string         // tool names (for cache compatibility)
	ToolsTrace       []ToolTraceEntry // detailed trace with success/fail status
	TotalTokens      int              // total LLM tokens used (input + output)
	InputTokens      int              // total input tokens across all iterations
	OutputTokens     int              // total output tokens across all iterations
	Model            string           // LLM model used for cost estimation
	InputTokenCost   float64          // $/1M input tokens from config (0 = auto from model)
	OutputTokenCost  float64          // $/1M output tokens from config (0 = auto from model)
	Iterations       int              // number of LLM iterations used
	CachedAt         time.Time
	ResolvedAt       *time.Time
}

// ToolTraceEntry records a tool invocation and its outcome.
type ToolTraceEntry struct {
	Name      string // e.g., "prometheus_query"
	Success   bool   // true = returned data, false = error/empty
	CallCount int    // number of calls to this tool category
}
