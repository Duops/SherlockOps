package domain

import "time"

// AnalysisResult holds the LLM analysis of an alert.
type AnalysisResult struct {
	AlertFingerprint string
	Text             string
	ToolsUsed        []string         // tool names (for cache compatibility)
	ToolsTrace       []ToolTraceEntry // detailed trace with success/fail status
	CachedAt         time.Time
	ResolvedAt       *time.Time
}

// ToolTraceEntry records a tool invocation and its outcome.
type ToolTraceEntry struct {
	Name    string // e.g., "prometheus_query"
	Success bool   // true = returned data, false = error/empty
}
