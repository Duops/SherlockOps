package tooling

import (
	"context"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

func TestKubernetesExecutor_ListTools(t *testing.T) {
	// ListTools does not need a live cluster.
	exec := &KubernetesExecutor{logger: testLogger()}

	tools, err := exec.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}

	expectedNames := map[string]bool{
		"k8s_get_pods":   true,
		"k8s_pod_logs":   true,
		"k8s_get_events": true,
	}
	for _, tool := range tools {
		if !expectedNames[tool.Name] {
			t.Errorf("unexpected tool name: %s", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has nil InputSchema", tool.Name)
		}
	}
}

func TestKubernetesExecutor_UnknownTool(t *testing.T) {
	exec := &KubernetesExecutor{logger: testLogger()}

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-1",
		Name: "k8s_nonexistent",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unknown tool")
	}
	if result.Content != "unknown tool: k8s_nonexistent" {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestKubernetesExecutor_PodLogs_MissingPod(t *testing.T) {
	exec := &KubernetesExecutor{logger: testLogger()}

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-2",
		Name:  "k8s_pod_logs",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing pod parameter")
	}
	if result.Content != "missing required parameter: pod" {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestStringParam(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		key      string
		defVal   string
		expected string
	}{
		{"existing value", map[string]interface{}{"ns": "monitoring"}, "ns", "default", "monitoring"},
		{"missing key", map[string]interface{}{}, "ns", "default", "default"},
		{"empty string", map[string]interface{}{"ns": ""}, "ns", "default", "default"},
		{"wrong type", map[string]interface{}{"ns": 123}, "ns", "default", "default"},
		{"nil input", nil, "ns", "default", "default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stringParam(tt.input, tt.key, tt.defVal)
			if result != tt.expected {
				t.Errorf("stringParam(%v, %q, %q) = %q, want %q", tt.input, tt.key, tt.defVal, result, tt.expected)
			}
		})
	}
}
