package tooling

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

func TestNewLokiExecutor(t *testing.T) {
	exec := NewLokiExecutor("http://localhost:3100/", "user", "pass", testLogger())
	if exec.url != "http://localhost:3100" {
		t.Errorf("expected trailing slash trimmed, got: %s", exec.url)
	}
	if exec.username != "user" || exec.password != "pass" {
		t.Error("credentials not stored correctly")
	}
}

func TestLokiExecutor_ListTools(t *testing.T) {
	exec := NewLokiExecutor("http://localhost:3100", "", "", testLogger())

	tools, err := exec.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has nil InputSchema", tool.Name)
		}
	}

	expected := []string{"loki_labels", "loki_label_values", "loki_query"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("expected tool %q not found", name)
		}
	}
}

func TestLokiExecutor_UnknownTool(t *testing.T) {
	exec := NewLokiExecutor("http://localhost:3100", "", "", testLogger())

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-1",
		Name: "loki_nonexistent",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unknown tool")
	}
	if !strings.Contains(result.Content, "unknown tool") {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestLokiExecutor_QueryMissingParam(t *testing.T) {
	exec := NewLokiExecutor("http://localhost:3100", "", "", testLogger())

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-2",
		Name:  "loki_query",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing query")
	}
	if !strings.Contains(result.Content, "missing required parameter: query") {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestLokiExecutor_LabelValuesMissingParam(t *testing.T) {
	exec := NewLokiExecutor("http://localhost:3100", "", "", testLogger())

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-3",
		Name:  "loki_label_values",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing label")
	}
	if !strings.Contains(result.Content, "missing required parameter: label") {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func newTestLokiServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/loki/api/v1/labels":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data":   []string{"app", "namespace", "pod"},
			})

		case strings.HasPrefix(r.URL.Path, "/loki/api/v1/label/") && strings.HasSuffix(r.URL.Path, "/values"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data":   []string{"monitoring", "default", "kube-system"},
			})

		case r.URL.Path == "/loki/api/v1/query_range":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "streams",
					"result": []map[string]interface{}{
						{
							"stream": map[string]string{
								"app":       "myapp",
								"namespace": "default",
							},
							"values": [][2]string{
								{"1234567890000000000", "error: something failed"},
								{"1234567891000000000", "info: recovery started"},
							},
						},
					},
				},
			})

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func TestLokiExecutor_Labels(t *testing.T) {
	srv := newTestLokiServer(t)
	defer srv.Close()

	exec := NewLokiExecutor(srv.URL, "", "", testLogger())
	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-labels",
		Name:  "loki_labels",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Available labels") {
		t.Errorf("expected 'Available labels' in result, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "namespace") {
		t.Errorf("expected 'namespace' in result, got: %s", result.Content)
	}
}

func TestLokiExecutor_LabelValues(t *testing.T) {
	srv := newTestLokiServer(t)
	defer srv.Close()

	exec := NewLokiExecutor(srv.URL, "", "", testLogger())
	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-lv",
		Name: "loki_label_values",
		Input: map[string]interface{}{
			"label": "namespace",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Values for label") {
		t.Errorf("expected 'Values for label' in result, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "monitoring") {
		t.Errorf("expected 'monitoring' in result, got: %s", result.Content)
	}
}

func TestLokiExecutor_Query(t *testing.T) {
	srv := newTestLokiServer(t)
	defer srv.Close()

	exec := NewLokiExecutor(srv.URL, "", "", testLogger())
	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-query",
		Name: "loki_query",
		Input: map[string]interface{}{
			"query": `{app="myapp"} |= "error"`,
			"start": "-1h",
			"end":   "now",
			"limit": float64(50),
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Streams: 1") {
		t.Errorf("expected 'Streams: 1' in result, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "error: something failed") {
		t.Errorf("expected log line in result, got: %s", result.Content)
	}
}

func TestLokiExecutor_Query_BasicAuth(t *testing.T) {
	var gotUser, gotPass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "streams",
				"result":     []interface{}{},
			},
		})
	}))
	defer srv.Close()

	exec := NewLokiExecutor(srv.URL, "loki-user", "loki-pass", testLogger())
	_, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-auth",
		Name: "loki_query",
		Input: map[string]interface{}{
			"query": `{app="test"}`,
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotUser != "loki-user" || gotPass != "loki-pass" {
		t.Errorf("expected loki-user:loki-pass, got %s:%s", gotUser, gotPass)
	}
}

func TestLokiExecutor_Query_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	exec := NewLokiExecutor(srv.URL, "", "", testLogger())
	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-err",
		Name: "loki_query",
		Input: map[string]interface{}{
			"query": `{app="test"}`,
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for 500 response")
	}
}

func TestLokiExecutor_Query_InvalidStartTime(t *testing.T) {
	exec := NewLokiExecutor("http://localhost:3100", "", "", testLogger())

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-bad-time",
		Name: "loki_query",
		Input: map[string]interface{}{
			"query": `{app="test"}`,
			"start": "not-a-time",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for invalid start time")
	}
	if !strings.Contains(result.Content, "invalid start time") {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestFormatLokiResponse_Success(t *testing.T) {
	data := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"resultType": "streams",
			"result": []map[string]interface{}{
				{
					"stream": map[string]string{"app": "myapp"},
					"values": [][2]string{
						{"1234567890000000000", "log line 1"},
					},
				},
			},
		},
	}
	body, _ := json.Marshal(data)
	result := formatLokiResponse(body)

	if !strings.Contains(result, "Streams: 1") {
		t.Errorf("expected 'Streams: 1' in result, got: %s", result)
	}
	if !strings.Contains(result, "log line 1") {
		t.Errorf("expected 'log line 1' in result, got: %s", result)
	}
}

func TestFormatLokiResponse_ParseError(t *testing.T) {
	result := formatLokiResponse([]byte("not json"))
	if !strings.Contains(result, "parse error") {
		t.Errorf("expected 'parse error' in result, got: %s", result)
	}
}

func TestFormatLokiResponse_NonSuccess(t *testing.T) {
	data := map[string]interface{}{
		"status": "error",
	}
	body, _ := json.Marshal(data)
	result := formatLokiResponse(body)

	if !strings.Contains(result, "Loki error status") {
		t.Errorf("expected 'Loki error status' in result, got: %s", result)
	}
}

func TestFormatStreamLabels(t *testing.T) {
	labels := map[string]string{
		"app": "test",
	}
	result := formatStreamLabels(labels)
	if !strings.Contains(result, `app="test"`) {
		t.Errorf("expected label in output, got: %s", result)
	}
	if !strings.HasPrefix(result, "{") || !strings.HasSuffix(result, "}") {
		t.Errorf("expected curly braces, got: %s", result)
	}
}
