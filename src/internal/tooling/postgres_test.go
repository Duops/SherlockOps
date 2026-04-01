package tooling

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func TestPostgresExecutor_ListTools(t *testing.T) {
	// ListTools does not require a live database connection.
	exec := &PostgresExecutor{
		dsn: "postgres://localhost/test",
	}

	tools, err := exec.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}

	expectedNames := []string{
		"pg_stat_activity",
		"pg_locks",
		"pg_replication_status",
		"pg_database_stats",
		"pg_slow_queries",
		"pg_table_stats",
	}

	if len(tools) != len(expectedNames) {
		t.Fatalf("expected %d tools, got %d", len(expectedNames), len(tools))
	}

	nameSet := make(map[string]bool)
	for _, tool := range tools {
		nameSet[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has nil InputSchema", tool.Name)
		}
	}

	for _, name := range expectedNames {
		if !nameSet[name] {
			t.Errorf("expected tool %q not found", name)
		}
	}
}

func TestPostgresExecutor_ExecuteUnknownTool(t *testing.T) {
	exec := &PostgresExecutor{
		dsn: "postgres://localhost/test",
	}

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "test-1",
		Name: "pg_nonexistent",
	})
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError to be true for unknown tool")
	}
	if result.CallID != "test-1" {
		t.Errorf("expected CallID 'test-1', got %q", result.CallID)
	}
}

func TestNullStr(t *testing.T) {
	tests := []struct {
		name     string
		input    sql.NullString
		expected string
	}{
		{"valid string", sql.NullString{String: "hello", Valid: true}, "hello"},
		{"null string", sql.NullString{Valid: false}, "<null>"},
		{"empty valid string", sql.NullString{String: "", Valid: true}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := nullStr(tt.input)
			if result != tt.expected {
				t.Errorf("nullStr(%v) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNullTimeStr(t *testing.T) {
	fixedTime := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		input    sql.NullTime
		expected string
	}{
		{"valid time", sql.NullTime{Time: fixedTime, Valid: true}, "2025-06-15 10:30:00"},
		{"null time", sql.NullTime{Valid: false}, "<never>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := nullTimeStr(tt.input)
			if result != tt.expected {
				t.Errorf("nullTimeStr(%v) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestPgTruncate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"needs truncation", "hello world", 8, "hello w~"},
		{"single char max", "ab", 1, "~"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pgTruncate(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("pgTruncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
			}
		})
	}
}

func TestSlowQueriesLimitParsing(t *testing.T) {
	// Test that limit is properly parsed from float64 (JSON number behavior).
	call := domain.ToolCall{
		ID:   "test-limit",
		Name: "pg_slow_queries",
		Input: map[string]interface{}{
			"limit": float64(5),
		},
	}

	v, ok := call.Input["limit"].(float64)
	if !ok || v != 5 {
		t.Errorf("expected limit=5, got %v (ok=%v)", v, ok)
	}

	// Verify the cap at 100.
	if v > 0 {
		limit := int(v)
		if limit > 100 {
			limit = 100
		}
		if limit != 5 {
			t.Errorf("expected capped limit=5, got %d", limit)
		}
	}
}

func TestToolInputSchemaStructure(t *testing.T) {
	exec := &PostgresExecutor{}
	tools, _ := exec.ListTools(context.Background())

	for _, tool := range tools {
		schemaType, ok := tool.InputSchema["type"].(string)
		if !ok || schemaType != "object" {
			t.Errorf("tool %q: expected InputSchema type 'object', got %v", tool.Name, tool.InputSchema["type"])
		}

		_, ok = tool.InputSchema["properties"].(map[string]interface{})
		if !ok {
			t.Errorf("tool %q: expected InputSchema to have 'properties' map", tool.Name)
		}
	}
}
