package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// LokiExecutor provides a Loki LogQL query tool.
type LokiExecutor struct {
	url      string
	username string
	password string
	client   *http.Client
	logger   *slog.Logger
}

// NewLokiExecutor creates a new Loki tool executor.
func NewLokiExecutor(url, username, password string, logger *slog.Logger) *LokiExecutor {
	return &LokiExecutor{
		url:      strings.TrimRight(url, "/"),
		username: username,
		password: password,
		client:   &http.Client{Timeout: 30 * time.Second},
		logger:   logger,
	}
}

// ListTools returns the Loki tool definitions.
func (l *LokiExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	return []domain.Tool{
		{
			Name:        "loki_labels",
			Description: "List available label names in Loki. Call this FIRST to discover what labels exist before building a LogQL query.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "loki_label_values",
			Description: "List values for a specific label in Loki. Use after loki_labels to find valid values for a label (e.g., list all 'namespace' values).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"label": map[string]interface{}{
						"type":        "string",
						"description": "Label name to get values for (e.g., 'namespace', 'app', 'pod')",
					},
				},
				"required": []interface{}{"label"},
			},
		},
		{
			Name:        "loki_query",
			Description: "Execute a LogQL query against Loki to search logs. Use loki_labels first to discover available labels.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "LogQL query expression, e.g. {namespace=\"monitoring\"} |= \"error\"",
					},
					"start": map[string]interface{}{
						"type":        "string",
						"description": "Start time, e.g. \"-30m\", \"-1h\", or RFC3339",
					},
					"end": map[string]interface{}{
						"type":        "string",
						"description": "End time, e.g. \"now\" or RFC3339",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Maximum number of log lines to return",
					},
				},
				"required": []interface{}{"query"},
			},
		},
	}, nil
}

// Execute runs the Loki tool call.
func (l *LokiExecutor) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	switch call.Name {
	case "loki_labels":
		return l.execLabels(ctx, call)
	case "loki_label_values":
		return l.execLabelValues(ctx, call)
	case "loki_query":
		// handled below
	default:
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unknown tool: %s", call.Name),
			IsError: true,
		}, nil
	}

	query, _ := call.Input["query"].(string)
	if query == "" {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: "missing required parameter: query",
			IsError: true,
		}, nil
	}

	now := time.Now()
	startStr, _ := call.Input["start"].(string)
	if startStr == "" {
		startStr = "-1h"
	}
	endStr, _ := call.Input["end"].(string)
	if endStr == "" {
		endStr = "now"
	}

	startTime, err := parseRelativeTime(startStr, now)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("invalid start time: %v", err),
			IsError: true,
		}, nil
	}
	endTime, err := parseRelativeTime(endStr, now)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("invalid end time: %v", err),
			IsError: true,
		}, nil
	}

	limit := 100
	if l, ok := call.Input["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	params := url.Values{
		"query": {query},
		"start": {strconv.FormatInt(startTime.UnixNano(), 10)},
		"end":   {strconv.FormatInt(endTime.UnixNano(), 10)},
		"limit": {strconv.Itoa(limit)},
	}

	u := l.url + "/loki/api/v1/query_range?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("create request error: %v", err),
			IsError: true,
		}, nil
	}

	if l.username != "" {
		req.SetBasicAuth(l.username, l.password)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("loki request error: %v", err),
			IsError: true,
		}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("read response error: %v", err),
			IsError: true,
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("loki error %d: %s", resp.StatusCode, string(body)),
			IsError: true,
		}, nil
	}

	formatted := formatLokiResponse(body)
	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

type lokiResponse struct {
	Status string   `json:"status"`
	Data   lokiData `json:"data"`
}

type lokiData struct {
	ResultType string             `json:"resultType"`
	Result     []lokiStreamResult `json:"result"`
}

type lokiStreamResult struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"` // [timestamp_ns, log_line]
}

func formatLokiResponse(body []byte) string {
	var resp lokiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Sprintf("parse error: %v\nRaw: %s", err, string(body))
	}

	if resp.Status != "success" {
		return fmt.Sprintf("Loki error status: %s", resp.Status)
	}

	var sb strings.Builder
	totalLines := 0
	for _, stream := range resp.Data.Result {
		totalLines += len(stream.Values)
	}
	sb.WriteString(fmt.Sprintf("Streams: %d, Total lines: %d\n\n", len(resp.Data.Result), totalLines))

	for _, stream := range resp.Data.Result {
		sb.WriteString(fmt.Sprintf("--- %s ---\n", formatStreamLabels(stream.Stream)))
		for _, entry := range stream.Values {
			ts, err := strconv.ParseInt(entry[0], 10, 64)
			if err == nil {
				t := time.Unix(0, ts).UTC().Format("15:04:05.000")
				sb.WriteString(fmt.Sprintf("[%s] %s\n", t, entry[1]))
			} else {
				sb.WriteString(fmt.Sprintf("[%s] %s\n", entry[0], entry[1]))
			}
		}
	}

	return sb.String()
}

func formatStreamLabels(labels map[string]string) string {
	var parts []string
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, k, v))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// execLabels calls GET /loki/api/v1/labels.
func (l *LokiExecutor) execLabels(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	u := l.url + "/loki/api/v1/labels"
	body, err := l.doGet(ctx, u)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: err.Error(), IsError: true}, nil
	}

	var resp struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("parse error: %v", err), IsError: true}, nil
	}

	return &domain.ToolResult{
		CallID:  call.ID,
		Content: fmt.Sprintf("Available labels (%d):\n%s", len(resp.Data), strings.Join(resp.Data, "\n")),
	}, nil
}

// execLabelValues calls GET /loki/api/v1/label/{name}/values.
func (l *LokiExecutor) execLabelValues(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	label, _ := call.Input["label"].(string)
	if label == "" {
		return &domain.ToolResult{CallID: call.ID, Content: "missing required parameter: label", IsError: true}, nil
	}

	u := l.url + "/loki/api/v1/label/" + url.PathEscape(label) + "/values"
	body, err := l.doGet(ctx, u)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: err.Error(), IsError: true}, nil
	}

	var resp struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("parse error: %v", err), IsError: true}, nil
	}

	result := fmt.Sprintf("Values for label '%s' (%d):\n%s", label, len(resp.Data), strings.Join(resp.Data, "\n"))
	return &domain.ToolResult{CallID: call.ID, Content: result}, nil
}

// doGet is a helper for simple GET requests to Loki API.
func (l *LokiExecutor) doGet(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if l.username != "" {
		req.SetBasicAuth(l.username, l.password)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}
