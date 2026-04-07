package domain

import "time"

// AnalysisResult holds the LLM analysis of an alert.
type AnalysisResult struct {
	AlertFingerprint string           `json:"alert_fingerprint"`
	AlertName        string           `json:"alert_name"`
	Source           string           `json:"source"`   // receiver source: "alertmanager", "grafana", ...
	Severity         string           `json:"severity"` // copied from the original alert
	Text             string           `json:"text"`
	ToolsUsed        []string         `json:"tools_used"`
	ToolsTrace       []ToolTraceEntry `json:"tools_trace"`
	TotalTokens      int              `json:"total_tokens"`
	InputTokens      int              `json:"input_tokens"`
	OutputTokens     int              `json:"output_tokens"`
	Model            string           `json:"model"`
	InputTokenCost   float64          `json:"input_token_cost"`
	OutputTokenCost  float64          `json:"output_token_cost"`
	Iterations       int              `json:"iterations"`
	CachedAt         time.Time        `json:"cached_at"`
	ResolvedAt       *time.Time       `json:"resolved_at,omitempty"`
}

// ToolTraceEntry records a tool invocation and its outcome.
type ToolTraceEntry struct {
	Name      string `json:"name"`
	Success   bool   `json:"success"`
	CallCount int    `json:"call_count"`
}
