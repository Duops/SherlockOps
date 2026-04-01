package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

const doBaseURL = "https://api.digitalocean.com/v2"

// DigitalOceanExecutor provides DigitalOcean API tools for droplets, Kubernetes,
// managed databases, monitoring metrics, and alert policies.
type DigitalOceanExecutor struct {
	token   string
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

// NewDigitalOceanExecutor creates a new DigitalOcean tool executor.
func NewDigitalOceanExecutor(token string, logger *slog.Logger) *DigitalOceanExecutor {
	return &DigitalOceanExecutor{
		token:   token,
		baseURL: doBaseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
		logger:  logger,
	}
}

// ListTools returns the available DigitalOcean tools.
func (d *DigitalOceanExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	return []domain.Tool{
		{
			Name:        "do_list_droplets",
			Description: "List DigitalOcean droplets, optionally filtered by tag name.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"tag_name": map[string]interface{}{
						"type":        "string",
						"description": "Filter droplets by tag name, e.g. \"web\"",
					},
				},
			},
		},
		{
			Name:        "do_droplet_details",
			Description: "Get full details of a single DigitalOcean droplet by its ID.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"droplet_id": map[string]interface{}{
						"type":        "string",
						"description": "Droplet ID, e.g. \"123\"",
					},
				},
				"required": []interface{}{"droplet_id"},
			},
		},
		{
			Name:        "do_monitoring_metrics",
			Description: "Get monitoring metrics for a DigitalOcean droplet. Supported metrics: cpu, memory_free, memory_available, disk_read, disk_write, bandwidth_in, bandwidth_out.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"host_id": map[string]interface{}{
						"type":        "string",
						"description": "Droplet ID to get metrics for",
					},
					"metric": map[string]interface{}{
						"type":        "string",
						"description": "Metric type: cpu, memory_free, memory_available, disk_read, disk_write, bandwidth_in, bandwidth_out",
					},
					"start": map[string]interface{}{
						"type":        "string",
						"description": "Start time, e.g. \"-1h\" or RFC3339",
					},
					"end": map[string]interface{}{
						"type":        "string",
						"description": "End time, e.g. \"now\" or RFC3339",
					},
				},
				"required": []interface{}{"host_id", "metric", "start", "end"},
			},
		},
		{
			Name:        "do_list_kubernetes_clusters",
			Description: "List DigitalOcean Kubernetes (DOKS) clusters with name, status, region, version, and node count.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "do_list_databases",
			Description: "List DigitalOcean managed database clusters with name, engine, status, size, region, and connection details.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "do_list_alerts",
			Description: "List configured DigitalOcean monitoring alert policies with type, description, value, and enabled status.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}, nil
}

// Execute runs a DigitalOcean tool call.
func (d *DigitalOceanExecutor) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	switch call.Name {
	case "do_list_droplets":
		return d.listDroplets(ctx, call)
	case "do_droplet_details":
		return d.dropletDetails(ctx, call)
	case "do_monitoring_metrics":
		return d.monitoringMetrics(ctx, call)
	case "do_list_kubernetes_clusters":
		return d.listKubernetesClusters(ctx, call)
	case "do_list_databases":
		return d.listDatabases(ctx, call)
	case "do_list_alerts":
		return d.listAlerts(ctx, call)
	default:
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unknown tool: %s", call.Name),
			IsError: true,
		}, nil
	}
}

// ---------------------------------------------------------------------------
// Tool implementations
// ---------------------------------------------------------------------------

func (d *DigitalOceanExecutor) listDroplets(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	path := "/droplets?per_page=50"
	if tag, _ := call.Input["tag_name"].(string); tag != "" {
		path += "&tag_name=" + tag
	}

	body, err := d.doRequest(ctx, path)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("DO API error: %v", err), IsError: true}, nil
	}

	var resp struct {
		Droplets []doDroplet `json:"droplets"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("parse error: %v", err), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatDroplets(resp.Droplets)}, nil
}

func (d *DigitalOceanExecutor) dropletDetails(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	dropletID, _ := call.Input["droplet_id"].(string)
	if dropletID == "" {
		return &domain.ToolResult{CallID: call.ID, Content: "missing required parameter: droplet_id", IsError: true}, nil
	}

	body, err := d.doRequest(ctx, "/droplets/"+dropletID)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("DO API error: %v", err), IsError: true}, nil
	}

	var resp struct {
		Droplet doDroplet `json:"droplet"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("parse error: %v", err), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatDropletDetail(resp.Droplet)}, nil
}

func (d *DigitalOceanExecutor) monitoringMetrics(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	hostID, _ := call.Input["host_id"].(string)
	metric, _ := call.Input["metric"].(string)
	startStr, _ := call.Input["start"].(string)
	endStr, _ := call.Input["end"].(string)

	if hostID == "" || metric == "" || startStr == "" || endStr == "" {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: "missing required parameters: host_id, metric, start, end",
			IsError: true,
		}, nil
	}

	metricEndpoint, ok := doMetricEndpoints[metric]
	if !ok {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unsupported metric %q; supported: cpu, memory_free, memory_available, disk_read, disk_write, bandwidth_in, bandwidth_out", metric),
			IsError: true,
		}, nil
	}

	now := time.Now()
	startTime, err := parseRelativeTime(startStr, now)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("invalid start time: %v", err), IsError: true}, nil
	}
	endTime, err := parseRelativeTime(endStr, now)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("invalid end time: %v", err), IsError: true}, nil
	}

	path := fmt.Sprintf("/monitoring/metrics/droplet/%s?host_id=%s&start=%d&end=%d",
		metricEndpoint, hostID, startTime.Unix(), endTime.Unix())

	body, err := d.doRequest(ctx, path)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("DO API error: %v", err), IsError: true}, nil
	}

	var resp doMetricsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("parse error: %v", err), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatDOMetrics(resp, metric, hostID)}, nil
}

func (d *DigitalOceanExecutor) listKubernetesClusters(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	body, err := d.doRequest(ctx, "/kubernetes/clusters?per_page=50")
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("DO API error: %v", err), IsError: true}, nil
	}

	var resp struct {
		KubernetesClusters []doK8sCluster `json:"kubernetes_clusters"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("parse error: %v", err), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatK8sClusters(resp.KubernetesClusters)}, nil
}

func (d *DigitalOceanExecutor) listDatabases(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	body, err := d.doRequest(ctx, "/databases?per_page=50")
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("DO API error: %v", err), IsError: true}, nil
	}

	var resp struct {
		Databases []doDatabase `json:"databases"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("parse error: %v", err), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatDatabases(resp.Databases)}, nil
}

func (d *DigitalOceanExecutor) listAlerts(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	body, err := d.doRequest(ctx, "/monitoring/alerts?per_page=50")
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("DO API error: %v", err), IsError: true}, nil
	}

	var resp struct {
		Policies []doAlertPolicy `json:"policies"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("parse error: %v", err), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatAlertPolicies(resp.Policies)}, nil
}

// ---------------------------------------------------------------------------
// HTTP helper
// ---------------------------------------------------------------------------

func (d *DigitalOceanExecutor) doRequest(ctx context.Context, path string) ([]byte, error) {
	reqURL := d.baseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+d.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
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

// ---------------------------------------------------------------------------
// DO API response types
// ---------------------------------------------------------------------------

type doDroplet struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Region struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"region"`
	Size struct {
		Slug   string `json:"slug"`
		Vcpus  int    `json:"vcpus"`
		Memory int    `json:"memory"`
		Disk   int    `json:"disk"`
	} `json:"size"`
	Networks struct {
		V4 []struct {
			IPAddress string `json:"ip_address"`
			Type      string `json:"type"`
		} `json:"v4"`
	} `json:"networks"`
	Image struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"image"`
	Tags      []string `json:"tags"`
	CreatedAt string   `json:"created_at"`
	VolumeIDs []string `json:"volume_ids"`
	VPCUUID   string   `json:"vpc_uuid"`
}

var doMetricEndpoints = map[string]string{
	"cpu":              "cpu",
	"memory_free":      "memory_free",
	"memory_available": "memory_available",
	"disk_read":        "disk_read",
	"disk_write":       "disk_write",
	"bandwidth_in":     "bandwidth_in",
	"bandwidth_out":    "bandwidth_out",
}

type doMetricsResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]interface{}   `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

type doK8sCluster struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Region        string `json:"region"`
	VersionSlug   string `json:"version_slug"`
	Status        struct {
		State   string `json:"state"`
		Message string `json:"message"`
	} `json:"status"`
	NodePools []struct {
		Name  string `json:"name"`
		Size  string `json:"size"`
		Count int    `json:"count"`
	} `json:"node_pools"`
	CreatedAt string `json:"created_at"`
}

type doDatabase struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Engine     string `json:"engine"`
	Version    string `json:"version"`
	Status     string `json:"status"`
	Size       string `json:"size"`
	Region     string `json:"region"`
	NumNodes   int    `json:"num_nodes"`
	Connection struct {
		URI      string `json:"uri"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Database string `json:"database"`
	} `json:"connection"`
	CreatedAt string `json:"created_at"`
}

type doAlertPolicy struct {
	UUID        string `json:"uuid"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Compare     string `json:"compare"`
	Value       int    `json:"value"`
	Window      string `json:"window"`
	Entities    []string `json:"entities"`
	Enabled     bool   `json:"enabled"`
}

// ---------------------------------------------------------------------------
// Response formatters
// ---------------------------------------------------------------------------

func formatDroplets(droplets []doDroplet) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Droplets: %d found\n\n", len(droplets)))

	for _, d := range droplets {
		sb.WriteString(fmt.Sprintf("Droplet: %s (ID: %d)\n", d.Name, d.ID))
		sb.WriteString(fmt.Sprintf("  Status: %s\n", d.Status))
		sb.WriteString(fmt.Sprintf("  Region: %s (%s)\n", d.Region.Slug, d.Region.Name))
		sb.WriteString(fmt.Sprintf("  Size: %s (vCPUs: %d, Memory: %d MB, Disk: %d GB)\n",
			d.Size.Slug, d.Size.Vcpus, d.Size.Memory, d.Size.Disk))

		for _, net := range d.Networks.V4 {
			sb.WriteString(fmt.Sprintf("  IP (%s): %s\n", net.Type, net.IPAddress))
		}
		if len(d.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("  Tags: %s\n", strings.Join(d.Tags, ", ")))
		}
		sb.WriteString("\n")
	}

	if len(droplets) == 0 {
		sb.WriteString("  (no droplets found)\n")
	}

	return sb.String()
}

func formatDropletDetail(d doDroplet) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Droplet: %s (ID: %d)\n", d.Name, d.ID))
	sb.WriteString(fmt.Sprintf("  Status: %s\n", d.Status))
	sb.WriteString(fmt.Sprintf("  Region: %s (%s)\n", d.Region.Slug, d.Region.Name))
	sb.WriteString(fmt.Sprintf("  Size: %s\n", d.Size.Slug))
	sb.WriteString(fmt.Sprintf("  vCPUs: %d\n", d.Size.Vcpus))
	sb.WriteString(fmt.Sprintf("  Memory: %d MB\n", d.Size.Memory))
	sb.WriteString(fmt.Sprintf("  Disk: %d GB\n", d.Size.Disk))
	sb.WriteString(fmt.Sprintf("  Image: %s\n", d.Image.Name))

	for _, net := range d.Networks.V4 {
		sb.WriteString(fmt.Sprintf("  IP (%s): %s\n", net.Type, net.IPAddress))
	}

	if len(d.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("  Tags: %s\n", strings.Join(d.Tags, ", ")))
	}
	if len(d.VolumeIDs) > 0 {
		sb.WriteString(fmt.Sprintf("  Volumes: %s\n", strings.Join(d.VolumeIDs, ", ")))
	}
	if d.VPCUUID != "" {
		sb.WriteString(fmt.Sprintf("  VPC: %s\n", d.VPCUUID))
	}
	if d.CreatedAt != "" {
		sb.WriteString(fmt.Sprintf("  Created: %s\n", d.CreatedAt))
	}

	return sb.String()
}

func formatDOMetrics(resp doMetricsResponse, metric, hostID string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Metrics: %s for droplet %s (status: %s)\n\n", metric, hostID, resp.Status))

	totalPoints := 0
	for _, result := range resp.Data.Result {
		totalPoints += len(result.Values)
	}

	sb.WriteString(fmt.Sprintf("Datapoints: %d\n\n", totalPoints))

	for _, result := range resp.Data.Result {
		if len(result.Metric) > 0 {
			var labels []string
			for k, v := range result.Metric {
				labels = append(labels, fmt.Sprintf("%s=%s", k, v))
			}
			sb.WriteString(fmt.Sprintf("Series: {%s}\n", strings.Join(labels, ", ")))
		}
		for _, val := range result.Values {
			if len(val) == 2 {
				ts, _ := val[0].(float64)
				value, _ := val[1].(string)
				t := time.Unix(int64(ts), 0).UTC().Format(time.RFC3339)
				sb.WriteString(fmt.Sprintf("  %s => %s\n", t, value))
			}
		}
	}

	if totalPoints == 0 {
		sb.WriteString("  (no datapoints in the requested time range)\n")
	}

	return sb.String()
}

func formatK8sClusters(clusters []doK8sCluster) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Kubernetes Clusters: %d found\n\n", len(clusters)))

	for _, c := range clusters {
		totalNodes := 0
		for _, pool := range c.NodePools {
			totalNodes += pool.Count
		}

		sb.WriteString(fmt.Sprintf("Cluster: %s\n", c.Name))
		sb.WriteString(fmt.Sprintf("  ID: %s\n", c.ID))
		sb.WriteString(fmt.Sprintf("  Status: %s\n", c.Status.State))
		if c.Status.Message != "" {
			sb.WriteString(fmt.Sprintf("  Message: %s\n", c.Status.Message))
		}
		sb.WriteString(fmt.Sprintf("  Region: %s\n", c.Region))
		sb.WriteString(fmt.Sprintf("  Version: %s\n", c.VersionSlug))
		sb.WriteString(fmt.Sprintf("  Nodes: %d\n", totalNodes))

		for _, pool := range c.NodePools {
			sb.WriteString(fmt.Sprintf("  Pool %q: %d x %s\n", pool.Name, pool.Count, pool.Size))
		}
		sb.WriteString("\n")
	}

	if len(clusters) == 0 {
		sb.WriteString("  (no Kubernetes clusters found)\n")
	}

	return sb.String()
}

func formatDatabases(databases []doDatabase) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Managed Databases: %d found\n\n", len(databases)))

	for _, db := range databases {
		sb.WriteString(fmt.Sprintf("Database: %s\n", db.Name))
		sb.WriteString(fmt.Sprintf("  ID: %s\n", db.ID))
		sb.WriteString(fmt.Sprintf("  Engine: %s %s\n", db.Engine, db.Version))
		sb.WriteString(fmt.Sprintf("  Status: %s\n", db.Status))
		sb.WriteString(fmt.Sprintf("  Size: %s\n", db.Size))
		sb.WriteString(fmt.Sprintf("  Region: %s\n", db.Region))
		sb.WriteString(fmt.Sprintf("  Nodes: %d\n", db.NumNodes))
		if db.Connection.Host != "" {
			sb.WriteString(fmt.Sprintf("  Host: %s\n", db.Connection.Host))
			sb.WriteString(fmt.Sprintf("  Port: %d\n", db.Connection.Port))
			sb.WriteString(fmt.Sprintf("  Database: %s\n", db.Connection.Database))
		}
		sb.WriteString("\n")
	}

	if len(databases) == 0 {
		sb.WriteString("  (no managed databases found)\n")
	}

	return sb.String()
}

func formatAlertPolicies(policies []doAlertPolicy) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Alert Policies: %d found\n\n", len(policies)))

	for _, p := range policies {
		sb.WriteString(fmt.Sprintf("Alert: %s\n", p.Type))
		sb.WriteString(fmt.Sprintf("  UUID: %s\n", p.UUID))
		if p.Description != "" {
			sb.WriteString(fmt.Sprintf("  Description: %s\n", p.Description))
		}
		sb.WriteString(fmt.Sprintf("  Condition: %s %d\n", p.Compare, p.Value))
		sb.WriteString(fmt.Sprintf("  Window: %s\n", p.Window))
		sb.WriteString(fmt.Sprintf("  Enabled: %t\n", p.Enabled))
		if len(p.Entities) > 0 {
			sb.WriteString(fmt.Sprintf("  Entities: %s\n", strings.Join(p.Entities, ", ")))
		}
		sb.WriteString("\n")
	}

	if len(policies) == 0 {
		sb.WriteString("  (no alert policies found)\n")
	}

	return sb.String()
}
