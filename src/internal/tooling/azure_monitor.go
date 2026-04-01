package tooling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// AzureMonitorExecutor provides Azure Monitor query and management tools.
type AzureMonitorExecutor struct {
	tenantID       string
	clientID       string
	clientSecret   string
	subscriptionID string
	accessToken    string
	tokenExpiry    time.Time
	mu             sync.Mutex
	client         *http.Client
	logger         *slog.Logger
}

// NewAzureMonitorExecutor creates a new Azure Monitor tool executor.
func NewAzureMonitorExecutor(tenantID, clientID, clientSecret, subscriptionID string, logger *slog.Logger) *AzureMonitorExecutor {
	return &AzureMonitorExecutor{
		tenantID:       tenantID,
		clientID:       clientID,
		clientSecret:   clientSecret,
		subscriptionID: subscriptionID,
		client:         &http.Client{Timeout: 30 * time.Second},
		logger:         logger,
	}
}

// ListTools returns the available Azure Monitor tools.
func (a *AzureMonitorExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	return []domain.Tool{
		{
			Name:        "azure_monitor_metrics",
			Description: "Query Azure Monitor metrics for a specific resource (CPU, memory, disk, network, etc.).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"resource_uri": map[string]interface{}{
						"type":        "string",
						"description": "Full ARM resource URI, e.g. /subscriptions/.../resourceGroups/.../providers/Microsoft.Compute/virtualMachines/myVM",
					},
					"metric_names": map[string]interface{}{
						"type":        "string",
						"description": "Comma-separated metric names, e.g. \"Percentage CPU,Available Memory Bytes\"",
					},
					"timespan": map[string]interface{}{
						"type":        "string",
						"description": "ISO 8601 duration for the query window, e.g. PT1H, PT6H, P1D",
					},
					"interval": map[string]interface{}{
						"type":        "string",
						"description": "Aggregation interval, e.g. PT1M, PT5M, PT1H",
					},
				},
				"required": []interface{}{"resource_uri", "metric_names"},
			},
		},
		{
			Name:        "azure_monitor_alerts",
			Description: "List active Azure Monitor alerts for the subscription, optionally filtered by severity and condition.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"severity": map[string]interface{}{
						"type":        "string",
						"description": "Filter by severity: Sev0, Sev1, Sev2, Sev3, Sev4",
					},
					"monitor_condition": map[string]interface{}{
						"type":        "string",
						"description": "Filter by monitor condition: Fired or Resolved",
					},
				},
			},
		},
		{
			Name:        "azure_log_analytics",
			Description: "Execute a Kusto (KQL) query against an Azure Log Analytics workspace.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "KQL query to execute, e.g. AzureActivity | where Level == 'Error' | take 50",
					},
					"workspace_id": map[string]interface{}{
						"type":        "string",
						"description": "Log Analytics workspace GUID",
					},
				},
				"required": []interface{}{"query", "workspace_id"},
			},
		},
		{
			Name:        "azure_vm_status",
			Description: "Get the instance view (power state, provisioning state, statuses) for an Azure VM.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"resource_group": map[string]interface{}{
						"type":        "string",
						"description": "Resource group name containing the VM",
					},
					"vm_name": map[string]interface{}{
						"type":        "string",
						"description": "Virtual machine name",
					},
				},
				"required": []interface{}{"resource_group", "vm_name"},
			},
		},
	}, nil
}

// Execute runs an Azure Monitor tool call.
func (a *AzureMonitorExecutor) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	switch call.Name {
	case "azure_monitor_metrics":
		return a.queryMetrics(ctx, call)
	case "azure_monitor_alerts":
		return a.listAlerts(ctx, call)
	case "azure_log_analytics":
		return a.queryLogAnalytics(ctx, call)
	case "azure_vm_status":
		return a.vmStatus(ctx, call)
	default:
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unknown tool: %s", call.Name),
			IsError: true,
		}, nil
	}
}

// ensureToken obtains or refreshes the Azure AD access token using client credentials.
func (a *AzureMonitorExecutor) ensureToken(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.accessToken != "" && time.Now().Before(a.tokenExpiry.Add(-60*time.Second)) {
		return nil
	}

	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", a.tenantID)

	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {a.clientID},
		"client_secret": {a.clientSecret},
		"scope":         {"https://management.azure.com/.default"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token request failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp azureTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return fmt.Errorf("unmarshal token response: %w", err)
	}

	a.accessToken = tokenResp.AccessToken
	a.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	a.logger.Debug("azure token acquired", "expires_in", tokenResp.ExpiresIn)
	return nil
}

// doAzureGet performs an authenticated GET against the Azure Management API.
func (a *AzureMonitorExecutor) doAzureGet(ctx context.Context, rawURL string) ([]byte, error) {
	if err := a.ensureToken(ctx); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.accessToken)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// doAzurePost performs an authenticated POST against a given URL.
func (a *AzureMonitorExecutor) doAzurePost(ctx context.Context, rawURL string, payload []byte) ([]byte, error) {
	if err := a.ensureToken(ctx); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// queryMetrics handles the azure_monitor_metrics tool.
func (a *AzureMonitorExecutor) queryMetrics(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	resourceURI, _ := call.Input["resource_uri"].(string)
	metricNames, _ := call.Input["metric_names"].(string)
	if resourceURI == "" || metricNames == "" {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: "missing required parameters: resource_uri, metric_names",
			IsError: true,
		}, nil
	}

	timespan, _ := call.Input["timespan"].(string)
	interval, _ := call.Input["interval"].(string)

	params := url.Values{
		"api-version": {"2024-02-01"},
		"metricnames": {metricNames},
	}
	if timespan != "" {
		params.Set("timespan", timespan)
	}
	if interval != "" {
		params.Set("interval", interval)
	}

	u := fmt.Sprintf("https://management.azure.com%s/providers/Microsoft.Insights/metrics?%s",
		resourceURI, params.Encode())

	body, err := a.doAzureGet(ctx, u)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("azure metrics error: %v", err),
			IsError: true,
		}, nil
	}

	formatted := formatMetricsResponse(body)
	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

// listAlerts handles the azure_monitor_alerts tool.
func (a *AzureMonitorExecutor) listAlerts(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	severity, _ := call.Input["severity"].(string)
	monitorCondition, _ := call.Input["monitor_condition"].(string)

	params := url.Values{
		"api-version": {"2023-01-01"},
	}
	if severity != "" {
		params.Set("severity", severity)
	}
	if monitorCondition != "" {
		params.Set("monitorCondition", monitorCondition)
	}

	u := fmt.Sprintf("https://management.azure.com/subscriptions/%s/providers/Microsoft.AlertsManagement/alerts?%s",
		a.subscriptionID, params.Encode())

	body, err := a.doAzureGet(ctx, u)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("azure alerts error: %v", err),
			IsError: true,
		}, nil
	}

	formatted := formatAlertsResponse(body)
	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

// queryLogAnalytics handles the azure_log_analytics tool.
func (a *AzureMonitorExecutor) queryLogAnalytics(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	query, _ := call.Input["query"].(string)
	workspaceID, _ := call.Input["workspace_id"].(string)
	if query == "" || workspaceID == "" {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: "missing required parameters: query, workspace_id",
			IsError: true,
		}, nil
	}

	payload, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("marshal error: %v", err),
			IsError: true,
		}, nil
	}

	u := fmt.Sprintf("https://api.loganalytics.io/v1/workspaces/%s/query", workspaceID)

	body, err := a.doAzurePost(ctx, u, payload)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("log analytics error: %v", err),
			IsError: true,
		}, nil
	}

	formatted := formatLogAnalyticsResponse(body)
	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

// vmStatus handles the azure_vm_status tool.
func (a *AzureMonitorExecutor) vmStatus(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	rg, _ := call.Input["resource_group"].(string)
	vmName, _ := call.Input["vm_name"].(string)
	if rg == "" || vmName == "" {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: "missing required parameters: resource_group, vm_name",
			IsError: true,
		}, nil
	}

	u := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s/instanceView?api-version=2024-03-01",
		a.subscriptionID, rg, vmName,
	)

	body, err := a.doAzureGet(ctx, u)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("azure vm status error: %v", err),
			IsError: true,
		}, nil
	}

	formatted := formatVMStatusResponse(body)
	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

// --- Azure API response types ---

type azureTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

type azureMetricsResponse struct {
	Value []azureMetricValue `json:"value"`
}

type azureMetricValue struct {
	Name       azureName          `json:"name"`
	Unit       string             `json:"unit"`
	Timeseries []azureTimeseries  `json:"timeseries"`
}

type azureName struct {
	Value          string `json:"value"`
	LocalizedValue string `json:"localizedValue"`
}

type azureTimeseries struct {
	Data []azureMetricData `json:"data"`
}

type azureMetricData struct {
	TimeStamp string   `json:"timeStamp"`
	Average   *float64 `json:"average,omitempty"`
	Maximum   *float64 `json:"maximum,omitempty"`
	Minimum   *float64 `json:"minimum,omitempty"`
	Total     *float64 `json:"total,omitempty"`
	Count     *float64 `json:"count,omitempty"`
}

type azureAlertsResponse struct {
	Value []azureAlertResource `json:"value"`
}

type azureAlertResource struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Properties azureAlertProperties `json:"properties"`
}

type azureAlertProperties struct {
	Severity         string `json:"severity"`
	MonitorCondition string `json:"monitorCondition"`
	AlertState       string `json:"alertState"`
	Description      string `json:"description"`
	TargetResource   string `json:"targetResource"`
	StartDateTime    string `json:"startDateTime"`
	Essentials       *azureAlertEssentials `json:"essentials,omitempty"`
}

type azureAlertEssentials struct {
	Severity         string `json:"severity"`
	MonitorCondition string `json:"monitorCondition"`
	AlertState       string `json:"alertState"`
	Description      string `json:"description"`
	TargetResource   string `json:"targetResource"`
	StartDateTime    string `json:"startDateTime"`
}

type azureLogAnalyticsResponse struct {
	Tables []azureLogTable `json:"tables"`
}

type azureLogTable struct {
	Name    string           `json:"name"`
	Columns []azureLogColumn `json:"columns"`
	Rows    [][]interface{}  `json:"rows"`
}

type azureLogColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type azureVMInstanceView struct {
	Statuses []azureVMStatus `json:"statuses"`
	VMAgent  *azureVMAgent   `json:"vmAgent,omitempty"`
}

type azureVMStatus struct {
	Code          string `json:"code"`
	Level         string `json:"level"`
	DisplayStatus string `json:"displayStatus"`
	Message       string `json:"message,omitempty"`
	Time          string `json:"time,omitempty"`
}

type azureVMAgent struct {
	VMAgentVersion string          `json:"vmAgentVersion"`
	Statuses       []azureVMStatus `json:"statuses"`
}

// --- Formatting helpers ---

func formatMetricsResponse(body []byte) string {
	var resp azureMetricsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Sprintf("Failed to parse metrics response: %v\nRaw: %s", err, truncate(string(body), 500))
	}

	if len(resp.Value) == 0 {
		return "No metrics returned."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Metrics (%d):\n\n", len(resp.Value)))

	for _, metric := range resp.Value {
		displayName := metric.Name.LocalizedValue
		if displayName == "" {
			displayName = metric.Name.Value
		}
		sb.WriteString(fmt.Sprintf("--- %s (unit: %s) ---\n", displayName, metric.Unit))

		for _, ts := range metric.Timeseries {
			if len(ts.Data) == 0 {
				sb.WriteString("  No data points.\n")
				continue
			}
			for _, dp := range ts.Data {
				sb.WriteString(fmt.Sprintf("  %s |", dp.TimeStamp))
				if dp.Average != nil {
					sb.WriteString(fmt.Sprintf(" avg=%.2f", *dp.Average))
				}
				if dp.Maximum != nil {
					sb.WriteString(fmt.Sprintf(" max=%.2f", *dp.Maximum))
				}
				if dp.Minimum != nil {
					sb.WriteString(fmt.Sprintf(" min=%.2f", *dp.Minimum))
				}
				if dp.Total != nil {
					sb.WriteString(fmt.Sprintf(" total=%.2f", *dp.Total))
				}
				if dp.Count != nil {
					sb.WriteString(fmt.Sprintf(" count=%.0f", *dp.Count))
				}
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func formatAlertsResponse(body []byte) string {
	var resp azureAlertsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Sprintf("Failed to parse alerts response: %v\nRaw: %s", err, truncate(string(body), 500))
	}

	if len(resp.Value) == 0 {
		return "No active alerts found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Azure Alerts (%d):\n\n", len(resp.Value)))

	for i, alert := range resp.Value {
		props := alert.Properties

		severity := props.Severity
		condition := props.MonitorCondition
		state := props.AlertState
		desc := props.Description
		target := props.TargetResource
		started := props.StartDateTime

		// Fall back to essentials if top-level properties are empty.
		if props.Essentials != nil {
			if severity == "" {
				severity = props.Essentials.Severity
			}
			if condition == "" {
				condition = props.Essentials.MonitorCondition
			}
			if state == "" {
				state = props.Essentials.AlertState
			}
			if desc == "" {
				desc = props.Essentials.Description
			}
			if target == "" {
				target = props.Essentials.TargetResource
			}
			if started == "" {
				started = props.Essentials.StartDateTime
			}
		}

		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, alert.Name))
		sb.WriteString(fmt.Sprintf("   Severity:  %s\n", severity))
		sb.WriteString(fmt.Sprintf("   Condition: %s\n", condition))
		sb.WriteString(fmt.Sprintf("   State:     %s\n", state))
		if target != "" {
			sb.WriteString(fmt.Sprintf("   Target:    %s\n", target))
		}
		if desc != "" {
			sb.WriteString(fmt.Sprintf("   Desc:      %s\n", desc))
		}
		if started != "" {
			sb.WriteString(fmt.Sprintf("   Started:   %s\n", started))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func formatLogAnalyticsResponse(body []byte) string {
	var resp azureLogAnalyticsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Sprintf("Failed to parse log analytics response: %v\nRaw: %s", err, truncate(string(body), 500))
	}

	if len(resp.Tables) == 0 {
		return "No tables returned."
	}

	var sb strings.Builder
	for _, table := range resp.Tables {
		sb.WriteString(fmt.Sprintf("Table: %s (%d rows)\n", table.Name, len(table.Rows)))

		// Column headers.
		colNames := make([]string, len(table.Columns))
		for i, col := range table.Columns {
			colNames[i] = col.Name
		}
		sb.WriteString(strings.Join(colNames, " | "))
		sb.WriteString("\n")
		sb.WriteString(strings.Repeat("-", 80))
		sb.WriteString("\n")

		// Rows (cap at 50 for readability).
		limit := len(table.Rows)
		if limit > 50 {
			limit = 50
		}
		for _, row := range table.Rows[:limit] {
			vals := make([]string, len(row))
			for i, v := range row {
				vals[i] = fmt.Sprintf("%v", v)
			}
			sb.WriteString(strings.Join(vals, " | "))
			sb.WriteString("\n")
		}
		if len(table.Rows) > 50 {
			sb.WriteString(fmt.Sprintf("... (%d more rows truncated)\n", len(table.Rows)-50))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func formatVMStatusResponse(body []byte) string {
	var view azureVMInstanceView
	if err := json.Unmarshal(body, &view); err != nil {
		return fmt.Sprintf("Failed to parse VM status response: %v\nRaw: %s", err, truncate(string(body), 500))
	}

	var sb strings.Builder
	sb.WriteString("VM Instance View:\n\n")

	if len(view.Statuses) == 0 {
		sb.WriteString("  No statuses reported.\n")
	}
	for _, s := range view.Statuses {
		sb.WriteString(fmt.Sprintf("  [%s] %s (level: %s)\n", s.Code, s.DisplayStatus, s.Level))
		if s.Message != "" {
			sb.WriteString(fmt.Sprintf("    Message: %s\n", s.Message))
		}
		if s.Time != "" {
			sb.WriteString(fmt.Sprintf("    Time:    %s\n", s.Time))
		}
	}

	if view.VMAgent != nil {
		sb.WriteString(fmt.Sprintf("\nVM Agent: %s\n", view.VMAgent.VMAgentVersion))
		for _, s := range view.VMAgent.Statuses {
			sb.WriteString(fmt.Sprintf("  [%s] %s (level: %s)\n", s.Code, s.DisplayStatus, s.Level))
			if s.Message != "" {
				sb.WriteString(fmt.Sprintf("    Message: %s\n", s.Message))
			}
		}
	}

	return sb.String()
}

// truncate limits a string to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
