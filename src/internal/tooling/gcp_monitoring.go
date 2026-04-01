package tooling

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

const (
	gcpTokenURL     = "https://oauth2.googleapis.com/token"
	gcpMonitoringV3 = "https://monitoring.googleapis.com/v3"
	gcpComputeV1    = "https://compute.googleapis.com/compute/v1"
	gcpLoggingV2    = "https://logging.googleapis.com/v2"
	gcpScope        = "https://www.googleapis.com/auth/cloud-platform"
)

// serviceAccountKey holds parsed service account JSON fields.
type serviceAccountKey struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
	TokenURI     string `json:"token_uri"`
}

// GCPMonitoringExecutor provides GCP Monitoring, Compute, and Logging tools
// using raw HTTP calls (no Google Cloud SDK).
type GCPMonitoringExecutor struct {
	projectID       string
	credentialsJSON string
	client          *http.Client
	logger          *slog.Logger

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
	saKey       *serviceAccountKey

	// Overridable base URLs for testing.
	tokenURL     string
	monitoringV3 string
	computeV1    string
	loggingV2    string
}

// NewGCPMonitoringExecutor creates a new GCP tool executor.
// credentialsJSON is either a path to a service account JSON file or the raw JSON content.
func NewGCPMonitoringExecutor(projectID, credentialsJSON string, logger *slog.Logger) *GCPMonitoringExecutor {
	return &GCPMonitoringExecutor{
		projectID:       projectID,
		credentialsJSON: credentialsJSON,
		client:          &http.Client{Timeout: 30 * time.Second},
		logger:          logger,
		tokenURL:        gcpTokenURL,
		monitoringV3:    gcpMonitoringV3,
		computeV1:       gcpComputeV1,
		loggingV2:       gcpLoggingV2,
	}
}

// ListTools returns all available GCP tools.
func (g *GCPMonitoringExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	return []domain.Tool{
		{
			Name:        "gcp_monitoring_query",
			Description: "Query GCP Cloud Monitoring time series using MQL (Monitoring Query Language).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "MQL query, e.g. fetch gce_instance :: compute.googleapis.com/instance/cpu/utilization | within 30m",
					},
				},
				"required": []interface{}{"query"},
			},
		},
		{
			Name:        "gcp_monitoring_list_alerts",
			Description: "List GCP Cloud Monitoring alert policies and their conditions.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filter": map[string]interface{}{
						"type":        "string",
						"description": "Optional filter for alert policies",
					},
				},
			},
		},
		{
			Name:        "gcp_logging_query",
			Description: "Query GCP Cloud Logging entries.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filter": map[string]interface{}{
						"type":        "string",
						"description": "Logging filter, e.g. resource.type=\"gce_instance\" AND severity>=ERROR",
					},
					"order_by": map[string]interface{}{
						"type":        "string",
						"description": "Order of results, e.g. \"timestamp desc\"",
					},
					"page_size": map[string]interface{}{
						"type":        "number",
						"description": "Maximum number of entries to return (default 100)",
					},
				},
				"required": []interface{}{"filter"},
			},
		},
		{
			Name:        "gcp_compute_instances",
			Description: "List or filter Google Compute Engine instances.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"zone": map[string]interface{}{
						"type":        "string",
						"description": "GCE zone, e.g. \"us-central1-a\". If empty, uses aggregated list across all zones.",
					},
					"filter": map[string]interface{}{
						"type":        "string",
						"description": "Optional filter expression, e.g. \"name=my-vm\"",
					},
				},
			},
		},
	}, nil
}

// Execute runs a GCP tool call.
func (g *GCPMonitoringExecutor) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	switch call.Name {
	case "gcp_monitoring_query":
		return g.queryTimeSeries(ctx, call)
	case "gcp_monitoring_list_alerts":
		return g.listAlertPolicies(ctx, call)
	case "gcp_logging_query":
		return g.queryLogs(ctx, call)
	case "gcp_compute_instances":
		return g.listInstances(ctx, call)
	default:
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unknown tool: %s", call.Name),
			IsError: true,
		}, nil
	}
}

// --- Authentication ---

// loadServiceAccountKey parses the service account credentials.
func (g *GCPMonitoringExecutor) loadServiceAccountKey() (*serviceAccountKey, error) {
	if g.saKey != nil {
		return g.saKey, nil
	}

	data := []byte(g.credentialsJSON)
	// If it looks like a file path rather than JSON, read the file.
	if len(data) > 0 && data[0] != '{' {
		var err error
		data, err = os.ReadFile(g.credentialsJSON)
		if err != nil {
			return nil, fmt.Errorf("read credentials file: %w", err)
		}
	}

	var key serviceAccountKey
	if err := json.Unmarshal(data, &key); err != nil {
		return nil, fmt.Errorf("parse credentials JSON: %w", err)
	}
	if key.ClientEmail == "" || key.PrivateKey == "" {
		return nil, fmt.Errorf("credentials missing client_email or private_key")
	}

	g.saKey = &key
	return &key, nil
}

// ensureToken returns a valid access token, refreshing if necessary.
func (g *GCPMonitoringExecutor) ensureToken(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.accessToken != "" && time.Now().Before(g.tokenExpiry) {
		return g.accessToken, nil
	}

	saKey, err := g.loadServiceAccountKey()
	if err != nil {
		return "", err
	}

	token, expiry, err := g.fetchAccessToken(ctx, saKey)
	if err != nil {
		return "", err
	}

	g.accessToken = token
	g.tokenExpiry = expiry
	return token, nil
}

// fetchAccessToken creates a signed JWT and exchanges it for an access token.
func (g *GCPMonitoringExecutor) fetchAccessToken(ctx context.Context, saKey *serviceAccountKey) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(time.Hour)

	signedJWT, err := g.createSignedJWT(saKey, now, exp)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("create JWT: %w", err)
	}

	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {signedJWT},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := g.client.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", time.Time{}, fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("empty access token in response")
	}

	// Subtract 60s to refresh before actual expiry.
	expiry := now.Add(time.Duration(tokenResp.ExpiresIn)*time.Second - 60*time.Second)
	return tokenResp.AccessToken, expiry, nil
}

// createSignedJWT builds and signs a JWT for the service account.
func (g *GCPMonitoringExecutor) createSignedJWT(saKey *serviceAccountKey, now, exp time.Time) (string, error) {
	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
	}
	headerJSON, _ := json.Marshal(header)

	tokenURI := saKey.TokenURI
	if tokenURI == "" {
		tokenURI = g.tokenURL
	}

	claims := map[string]interface{}{
		"iss":   saKey.ClientEmail,
		"scope": gcpScope,
		"aud":   tokenURI,
		"iat":   now.Unix(),
		"exp":   exp.Unix(),
	}
	claimsJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	unsigned := headerB64 + "." + claimsB64

	block, _ := pem.Decode([]byte(saKey.PrivateKey))
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM block from private key")
	}

	privKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}

	rsaKey, ok := privKey.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("private key is not RSA")
	}

	hash := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(nil, rsaKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return unsigned + "." + sigB64, nil
}

// --- HTTP helpers ---

func (g *GCPMonitoringExecutor) doAuthGet(ctx context.Context, fullURL string) ([]byte, error) {
	token, err := g.ensureToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := g.client.Do(req)
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

func (g *GCPMonitoringExecutor) doAuthPost(ctx context.Context, fullURL string, payload interface{}) ([]byte, error) {
	token, err := g.ensureToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
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

// --- Tool: gcp_monitoring_query ---

func (g *GCPMonitoringExecutor) queryTimeSeries(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	query, _ := call.Input["query"].(string)
	if query == "" {
		return &domain.ToolResult{CallID: call.ID, Content: "missing required parameter: query", IsError: true}, nil
	}

	u := fmt.Sprintf("%s/projects/%s/timeSeries:query", g.monitoringV3, g.projectID)

	payload := map[string]string{
		"query": query,
	}

	body, err := g.doAuthPost(ctx, u, payload)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("monitoring query error: %v", err), IsError: true}, nil
	}

	formatted := formatTimeSeriesResponse(body)
	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

// --- Tool: gcp_monitoring_list_alerts ---

func (g *GCPMonitoringExecutor) listAlertPolicies(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	params := url.Values{}
	if filter, _ := call.Input["filter"].(string); filter != "" {
		params.Set("filter", filter)
	}

	u := fmt.Sprintf("%s/projects/%s/alertPolicies", g.monitoringV3, g.projectID)
	if encoded := params.Encode(); encoded != "" {
		u += "?" + encoded
	}

	body, err := g.doAuthGet(ctx, u)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("list alerts error: %v", err), IsError: true}, nil
	}

	formatted := formatAlertPoliciesResponse(body)
	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

// --- Tool: gcp_compute_list_instances ---

func (g *GCPMonitoringExecutor) listInstances(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	zone, _ := call.Input["zone"].(string)
	filter, _ := call.Input["filter"].(string)

	var u string
	if zone != "" {
		u = fmt.Sprintf("%s/projects/%s/zones/%s/instances", g.computeV1, g.projectID, zone)
	} else {
		u = fmt.Sprintf("%s/projects/%s/aggregated/instances", g.computeV1, g.projectID)
	}

	if filter != "" {
		u += "?" + url.Values{"filter": {filter}}.Encode()
	}

	body, err := g.doAuthGet(ctx, u)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("list instances error: %v", err), IsError: true}, nil
	}

	formatted := formatInstancesResponse(body, zone)
	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

// --- Tool: gcp_logging_query ---

func (g *GCPMonitoringExecutor) queryLogs(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	filter, _ := call.Input["filter"].(string)
	if filter == "" {
		return &domain.ToolResult{CallID: call.ID, Content: "missing required parameter: filter", IsError: true}, nil
	}

	orderBy, _ := call.Input["order_by"].(string)
	if orderBy == "" {
		orderBy = "timestamp desc"
	}

	pageSize := 100
	if ps, ok := call.Input["page_size"].(float64); ok && ps > 0 {
		pageSize = int(ps)
	}

	payload := map[string]interface{}{
		"resourceNames": []string{fmt.Sprintf("projects/%s", g.projectID)},
		"filter":        filter,
		"orderBy":       orderBy,
		"pageSize":      pageSize,
	}

	u := fmt.Sprintf("%s/entries:list", g.loggingV2)

	body, err := g.doAuthPost(ctx, u, payload)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("logging query error: %v", err), IsError: true}, nil
	}

	formatted := formatLogEntriesResponse(body)
	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

// --- Response formatters ---

func formatTimeSeriesResponse(body []byte) string {
	var resp struct {
		TimeSeries []struct {
			Metric struct {
				Type   string            `json:"type"`
				Labels map[string]string `json:"labels"`
			} `json:"metric"`
			Resource struct {
				Type   string            `json:"type"`
				Labels map[string]string `json:"labels"`
			} `json:"resource"`
			Points []struct {
				Interval struct {
					StartTime string `json:"startTime"`
					EndTime   string `json:"endTime"`
				} `json:"interval"`
				Value struct {
					DoubleValue  *float64 `json:"doubleValue"`
					Int64Value   *string  `json:"int64Value"`
					BoolValue    *bool    `json:"boolValue"`
					StringValue  *string  `json:"stringValue"`
				} `json:"value"`
			} `json:"points"`
		} `json:"timeSeries"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Sprintf("Failed to parse response: %v\nRaw: %s", err, gcpTruncate(string(body), 500))
	}

	if len(resp.TimeSeries) == 0 {
		return "No time series data found for the given filter and interval."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Time series results: %d series\n\n", len(resp.TimeSeries)))

	for i, ts := range resp.TimeSeries {
		sb.WriteString(fmt.Sprintf("--- Series %d ---\n", i+1))
		sb.WriteString(fmt.Sprintf("Metric: %s\n", ts.Metric.Type))
		if len(ts.Metric.Labels) > 0 {
			sb.WriteString(fmt.Sprintf("Metric Labels: %s\n", formatLabels(ts.Metric.Labels)))
		}
		sb.WriteString(fmt.Sprintf("Resource: %s\n", ts.Resource.Type))
		if len(ts.Resource.Labels) > 0 {
			sb.WriteString(fmt.Sprintf("Resource Labels: %s\n", formatLabels(ts.Resource.Labels)))
		}
		sb.WriteString(fmt.Sprintf("Points: %d\n", len(ts.Points)))

		for _, pt := range ts.Points {
			val := formatPointValue(pt.Value.DoubleValue, pt.Value.Int64Value, pt.Value.BoolValue, pt.Value.StringValue)
			sb.WriteString(fmt.Sprintf("  %s => %s\n", pt.Interval.EndTime, val))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func formatAlertPoliciesResponse(body []byte) string {
	var resp struct {
		AlertPolicies []struct {
			Name         string `json:"name"`
			DisplayName  string `json:"displayName"`
			Enabled      struct {
				Value bool `json:"value"`
			} `json:"enabled"`
			Conditions []struct {
				DisplayName string `json:"displayName"`
				Name        string `json:"name"`
			} `json:"conditions"`
			Documentation struct {
				Content string `json:"content"`
			} `json:"documentation"`
		} `json:"alertPolicies"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Sprintf("Failed to parse response: %v\nRaw: %s", err, gcpTruncate(string(body), 500))
	}

	if len(resp.AlertPolicies) == 0 {
		return "No alert policies found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Alert policies: %d\n\n", len(resp.AlertPolicies)))

	for _, policy := range resp.AlertPolicies {
		enabledStr := "DISABLED"
		if policy.Enabled.Value {
			enabledStr = "ENABLED"
		}
		sb.WriteString(fmt.Sprintf("Policy: %s [%s]\n", policy.DisplayName, enabledStr))
		sb.WriteString(fmt.Sprintf("  Name: %s\n", policy.Name))
		for _, cond := range policy.Conditions {
			sb.WriteString(fmt.Sprintf("  Condition: %s\n", cond.DisplayName))
		}
		if policy.Documentation.Content != "" {
			sb.WriteString(fmt.Sprintf("  Documentation: %s\n", gcpTruncate(policy.Documentation.Content, 200)))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func formatInstancesResponse(body []byte, zone string) string {
	if zone != "" {
		return formatZoneInstancesResponse(body)
	}
	return formatAggregatedInstancesResponse(body)
}

func formatZoneInstancesResponse(body []byte) string {
	var resp struct {
		Items []gcpInstance `json:"items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Sprintf("Failed to parse response: %v\nRaw: %s", err, gcpTruncate(string(body), 500))
	}

	if len(resp.Items) == 0 {
		return "No instances found."
	}

	return formatInstanceList(resp.Items)
}

func formatAggregatedInstancesResponse(body []byte) string {
	var resp struct {
		Items map[string]struct {
			Instances []gcpInstance `json:"instances"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Sprintf("Failed to parse response: %v\nRaw: %s", err, gcpTruncate(string(body), 500))
	}

	var all []gcpInstance
	for _, scopedList := range resp.Items {
		all = append(all, scopedList.Instances...)
	}

	if len(all) == 0 {
		return "No instances found across any zone."
	}

	return formatInstanceList(all)
}

type gcpInstance struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	MachineType string `json:"machineType"`
	Zone        string `json:"zone"`
	NetworkInterfaces []struct {
		NetworkIP    string `json:"networkIP"`
		AccessConfigs []struct {
			NatIP string `json:"natIP"`
		} `json:"accessConfigs"`
	} `json:"networkInterfaces"`
}

func formatInstanceList(instances []gcpInstance) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Instances: %d\n\n", len(instances)))

	for _, inst := range instances {
		// Extract short machine type and zone from full URLs.
		machineType := lastSegment(inst.MachineType)
		zone := lastSegment(inst.Zone)

		var internalIP, externalIP string
		if len(inst.NetworkInterfaces) > 0 {
			internalIP = inst.NetworkInterfaces[0].NetworkIP
			if len(inst.NetworkInterfaces[0].AccessConfigs) > 0 {
				externalIP = inst.NetworkInterfaces[0].AccessConfigs[0].NatIP
			}
		}

		sb.WriteString(fmt.Sprintf("%-30s  %-10s  %-20s  %-20s  internal=%s  external=%s\n",
			inst.Name, inst.Status, machineType, zone, internalIP, externalIP))
	}

	return sb.String()
}

func formatLogEntriesResponse(body []byte) string {
	var resp struct {
		Entries []struct {
			Timestamp   string          `json:"timestamp"`
			Severity    string          `json:"severity"`
			LogName     string          `json:"logName"`
			TextPayload string          `json:"textPayload"`
			JSONPayload json.RawMessage `json:"jsonPayload"`
			Resource    struct {
				Type   string            `json:"type"`
				Labels map[string]string `json:"labels"`
			} `json:"resource"`
		} `json:"entries"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Sprintf("Failed to parse response: %v\nRaw: %s", err, gcpTruncate(string(body), 500))
	}

	if len(resp.Entries) == 0 {
		return "No log entries found for the given filter."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Log entries: %d\n\n", len(resp.Entries)))

	for _, entry := range resp.Entries {
		sb.WriteString(fmt.Sprintf("[%s] %s  %s\n", entry.Severity, entry.Timestamp, lastSegment(entry.LogName)))

		if entry.TextPayload != "" {
			sb.WriteString(fmt.Sprintf("  %s\n", gcpTruncate(entry.TextPayload, 300)))
		} else if len(entry.JSONPayload) > 0 {
			sb.WriteString(fmt.Sprintf("  %s\n", gcpTruncate(string(entry.JSONPayload), 300)))
		}

		sb.WriteString(fmt.Sprintf("  Resource: %s %s\n\n", entry.Resource.Type, formatLabels(entry.Resource.Labels)))
	}

	return sb.String()
}

// --- Helpers ---

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("%s=%q", k, v))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func formatPointValue(dv *float64, iv *string, bv *bool, sv *string) string {
	if dv != nil {
		return strconv.FormatFloat(*dv, 'f', -1, 64)
	}
	if iv != nil {
		return *iv
	}
	if bv != nil {
		return strconv.FormatBool(*bv)
	}
	if sv != nil {
		return *sv
	}
	return "null"
}

func lastSegment(path string) string {
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func gcpTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
