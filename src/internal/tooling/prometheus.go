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

// PrometheusExecutor provides Prometheus/VictoriaMetrics query tools.
type PrometheusExecutor struct {
	url      string
	username string
	password string
	client   *http.Client
	logger   *slog.Logger
}

// NewPrometheusExecutor creates a new Prometheus tool executor.
func NewPrometheusExecutor(url, username, password string, logger *slog.Logger) *PrometheusExecutor {
	return &PrometheusExecutor{
		url:      strings.TrimRight(url, "/"),
		username: username,
		password: password,
		client:   &http.Client{Timeout: 30 * time.Second},
		logger:   logger,
	}
}

// ListTools returns the available Prometheus tools.
func (p *PrometheusExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	return []domain.Tool{
		{
			Name:        "prometheus_labels",
			Description: "List available metric label names in Prometheus/VictoriaMetrics. Call this FIRST to discover what labels exist.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "prometheus_label_values",
			Description: "List values for a specific label. Use after prometheus_labels to find valid values (e.g., list all 'job' or 'namespace' values).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"label": map[string]interface{}{
						"type":        "string",
						"description": "Label name (e.g., 'job', 'namespace', 'instance')",
					},
				},
				"required": []interface{}{"label"},
			},
		},
		{
			Name:        "prometheus_series",
			Description: "Find metric names matching a selector. Use to discover what metrics exist for a job/namespace.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"match": map[string]interface{}{
						"type":        "string",
						"description": "Series selector, e.g. {job=\"node-exporter\"} or {__name__=~\"node_.*\"}",
					},
				},
				"required": []interface{}{"match"},
			},
		},
		{
			Name:        "prometheus_query",
			Description: "Execute an instant PromQL query. Use prometheus_labels/prometheus_series first to discover available metrics and labels.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "PromQL query expression",
					},
				},
				"required": []interface{}{"query"},
			},
		},
		{
			Name:        "prometheus_query_range",
			Description: "Execute a range PromQL query against Prometheus/VictoriaMetrics.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "PromQL query expression",
					},
					"start": map[string]interface{}{
						"type":        "string",
						"description": "Start time, e.g. \"-30m\", \"-1h\", or RFC3339 timestamp",
					},
					"end": map[string]interface{}{
						"type":        "string",
						"description": "End time, e.g. \"now\" or RFC3339 timestamp",
					},
					"step": map[string]interface{}{
						"type":        "string",
						"description": "Query resolution step, e.g. \"60s\", \"5m\"",
					},
				},
				"required": []interface{}{"query", "start", "end", "step"},
			},
		},
	}, nil
}

// Execute runs a Prometheus tool call.
func (p *PrometheusExecutor) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	switch call.Name {
	case "prometheus_labels":
		return p.execLabels(ctx, call)
	case "prometheus_label_values":
		return p.execLabelValues(ctx, call)
	case "prometheus_series":
		return p.execSeries(ctx, call)
	}
	switch call.Name {
	case "prometheus_query":
		return p.instantQuery(ctx, call)
	case "prometheus_query_range":
		return p.rangeQuery(ctx, call)
	default:
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unknown tool: %s", call.Name),
			IsError: true,
		}, nil
	}
}

func (p *PrometheusExecutor) instantQuery(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	query, _ := call.Input["query"].(string)
	if query == "" {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: "missing required parameter: query",
			IsError: true,
		}, nil
	}

	params := url.Values{"query": {query}}
	body, err := p.doGet(ctx, "/api/v1/query", params)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("prometheus query error: %v", err),
			IsError: true,
		}, nil
	}

	formatted, err := formatPrometheusResponse(body)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("format error: %v\nRaw: %s", err, string(body)),
			IsError: true,
		}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

func (p *PrometheusExecutor) rangeQuery(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	query, _ := call.Input["query"].(string)
	startStr, _ := call.Input["start"].(string)
	endStr, _ := call.Input["end"].(string)
	step, _ := call.Input["step"].(string)

	if query == "" || startStr == "" || endStr == "" || step == "" {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: "missing required parameters: query, start, end, step",
			IsError: true,
		}, nil
	}

	now := time.Now()
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

	params := url.Values{
		"query": {query},
		"start": {strconv.FormatFloat(float64(startTime.Unix()), 'f', 0, 64)},
		"end":   {strconv.FormatFloat(float64(endTime.Unix()), 'f', 0, 64)},
		"step":  {step},
	}

	body, err := p.doGet(ctx, "/api/v1/query_range", params)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("prometheus query_range error: %v", err),
			IsError: true,
		}, nil
	}

	formatted, err := formatPrometheusResponse(body)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("format error: %v\nRaw: %s", err, string(body)),
			IsError: true,
		}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

func (p *PrometheusExecutor) doGet(ctx context.Context, path string, params url.Values) ([]byte, error) {
	u := p.url + path + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if p.username != "" {
		req.SetBasicAuth(p.username, p.password)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// parseRelativeTime parses relative time strings like "-30m", "-1h", "now",
// or absolute RFC3339 timestamps.
func parseRelativeTime(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "now" {
		return now, nil
	}

	if strings.HasPrefix(s, "-") {
		d, err := time.ParseDuration(s[1:])
		if err != nil {
			return time.Time{}, fmt.Errorf("parse duration %q: %w", s, err)
		}
		return now.Add(-d), nil
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", s, err)
	}
	return t, nil
}

// prometheusAPIResponse is the standard Prometheus API response envelope.
type prometheusAPIResponse struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  string          `json:"error,omitempty"`
}

type prometheusData struct {
	ResultType string            `json:"resultType"`
	Result     []json.RawMessage `json:"result"`
}

type prometheusVectorResult struct {
	Metric map[string]string `json:"metric"`
	Value  [2]interface{}    `json:"value"`
}

type prometheusMatrixResult struct {
	Metric map[string]string `json:"metric"`
	Values [][2]interface{}  `json:"values"`
}

func formatPrometheusResponse(body []byte) (string, error) {
	var apiResp prometheusAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return "", fmt.Errorf("unmarshal: %w", err)
	}

	if apiResp.Status != "success" {
		return fmt.Sprintf("Prometheus error: %s", apiResp.Error), nil
	}

	var data prometheusData
	if err := json.Unmarshal(apiResp.Data, &data); err != nil {
		return "", fmt.Errorf("unmarshal data: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Result type: %s, Series: %d\n\n", data.ResultType, len(data.Result)))

	switch data.ResultType {
	case "vector":
		for _, raw := range data.Result {
			var r prometheusVectorResult
			if err := json.Unmarshal(raw, &r); err != nil {
				continue
			}
			sb.WriteString(fmt.Sprintf("%s => %v\n", formatMetric(r.Metric), r.Value[1]))
		}
	case "matrix":
		for _, raw := range data.Result {
			var r prometheusMatrixResult
			if err := json.Unmarshal(raw, &r); err != nil {
				continue
			}
			sb.WriteString(fmt.Sprintf("%s:\n", formatMetric(r.Metric)))
			for _, v := range r.Values {
				ts, _ := v[0].(float64)
				t := time.Unix(int64(ts), 0).UTC().Format(time.RFC3339)
				sb.WriteString(fmt.Sprintf("  %s => %v\n", t, v[1]))
			}
		}
	case "scalar", "string":
		sb.WriteString(string(body))
	}

	return sb.String(), nil
}

func formatMetric(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	name := m["__name__"]
	var parts []string
	for k, v := range m {
		if k == "__name__" {
			continue
		}
		parts = append(parts, fmt.Sprintf(`%s="%s"`, k, v))
	}
	if name != "" {
		return fmt.Sprintf("%s{%s}", name, strings.Join(parts, ", "))
	}
	return fmt.Sprintf("{%s}", strings.Join(parts, ", "))
}

func (p *PrometheusExecutor) execLabels(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	body, err := p.doGet(ctx, "/api/v1/labels", url.Values{})
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

func (p *PrometheusExecutor) execLabelValues(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	label, _ := call.Input["label"].(string)
	if label == "" {
		return &domain.ToolResult{CallID: call.ID, Content: "missing required parameter: label", IsError: true}, nil
	}
	body, err := p.doGet(ctx, "/api/v1/label/"+url.PathEscape(label)+"/values", url.Values{})
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
	// Limit to 50 values to avoid flooding LLM context.
	data := resp.Data
	suffix := ""
	if len(data) > 50 {
		suffix = fmt.Sprintf("\n... and %d more", len(data)-50)
		data = data[:50]
	}
	return &domain.ToolResult{
		CallID:  call.ID,
		Content: fmt.Sprintf("Values for '%s' (%d):\n%s%s", label, len(resp.Data), strings.Join(data, "\n"), suffix),
	}, nil
}

func (p *PrometheusExecutor) execSeries(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	match, _ := call.Input["match"].(string)
	if match == "" {
		return &domain.ToolResult{CallID: call.ID, Content: "missing required parameter: match", IsError: true}, nil
	}
	params := url.Values{"match[]": {match}}
	now := time.Now()
	params.Set("start", strconv.FormatInt(now.Add(-30*time.Minute).Unix(), 10))
	params.Set("end", strconv.FormatInt(now.Unix(), 10))

	body, err := p.doGet(ctx, "/api/v1/series", params)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: err.Error(), IsError: true}, nil
	}
	var resp struct {
		Status string              `json:"status"`
		Data   []map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("parse error: %v", err), IsError: true}, nil
	}

	// Collect unique metric names.
	names := make(map[string]bool)
	for _, s := range resp.Data {
		if n, ok := s["__name__"]; ok {
			names[n] = true
		}
	}
	var nameList []string
	for n := range names {
		nameList = append(nameList, n)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d series, %d unique metrics:\n", len(resp.Data), len(nameList)))
	for _, n := range nameList {
		sb.WriteString(n + "\n")
	}
	return &domain.ToolResult{CallID: call.ID, Content: sb.String()}, nil
}

