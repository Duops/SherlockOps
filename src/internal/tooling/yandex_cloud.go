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

	"github.com/Duops/SherlockOps/internal/domain"
)

// YandexCloudExecutor provides Yandex Cloud tools: Compute, Monitoring, Managed DB, VPC.
type YandexCloudExecutor struct {
	cloudID   string
	folderID  string
	token     string
	tokenType string // "iam" or "oauth"
	client    *http.Client
	logger    *slog.Logger
}

// NewYandexCloudExecutor creates a new Yandex Cloud tool executor.
func NewYandexCloudExecutor(cloudID, folderID, token, tokenType string, logger *slog.Logger) *YandexCloudExecutor {
	return &YandexCloudExecutor{
		cloudID:   cloudID,
		folderID:  folderID,
		token:     token,
		tokenType: tokenType,
		client:    &http.Client{Timeout: 30 * time.Second},
		logger:    logger,
	}
}

// ListTools returns the available Yandex Cloud tools.
func (y *YandexCloudExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	return []domain.Tool{
		{
			Name:        "yc_compute_list_instances",
			Description: "List Compute instances in a Yandex Cloud folder with optional filter.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"folder_id": map[string]interface{}{
						"type":        "string",
						"description": "Folder ID (defaults to executor's folderID)",
					},
					"filter": map[string]interface{}{
						"type":        "string",
						"description": "Filter expression, e.g. name='my-vm'",
					},
				},
			},
		},
		{
			Name:        "yc_compute_instance_details",
			Description: "Get full details of a specific Compute instance by ID.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"instance_id": map[string]interface{}{
						"type":        "string",
						"description": "Instance ID",
					},
				},
				"required": []interface{}{"instance_id"},
			},
		},
		{
			Name:        "yc_monitoring_read",
			Description: "Read monitoring metrics from Yandex Monitoring using a query.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Monitoring query, e.g. cpu_usage{service='compute', resource_id='xxx'}",
					},
					"from": map[string]interface{}{
						"type":        "string",
						"description": "Start time, e.g. \"-1h\" or RFC3339",
					},
					"to": map[string]interface{}{
						"type":        "string",
						"description": "End time, e.g. \"now\" or RFC3339",
					},
					"folder_id": map[string]interface{}{
						"type":        "string",
						"description": "Folder ID (defaults to executor's folderID)",
					},
				},
				"required": []interface{}{"query", "from", "to"},
			},
		},
		{
			Name:        "yc_managed_db_list",
			Description: "List Managed Database clusters (PostgreSQL, MySQL, Redis, MongoDB, ClickHouse, Kafka) in a folder.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"service": map[string]interface{}{
						"type":        "string",
						"description": "Database service: postgresql, mysql, redis, mongodb, clickhouse, kafka",
					},
					"folder_id": map[string]interface{}{
						"type":        "string",
						"description": "Folder ID (defaults to executor's folderID)",
					},
				},
				"required": []interface{}{"service"},
			},
		},
		{
			Name:        "yc_managed_db_hosts",
			Description: "List hosts of a Managed Database cluster.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"service": map[string]interface{}{
						"type":        "string",
						"description": "Database service: postgresql, mysql, redis, mongodb, clickhouse, kafka",
					},
					"cluster_id": map[string]interface{}{
						"type":        "string",
						"description": "Cluster ID",
					},
				},
				"required": []interface{}{"service", "cluster_id"},
			},
		},
		{
			Name:        "yc_vpc_list",
			Description: "List VPC networks in a Yandex Cloud folder.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"folder_id": map[string]interface{}{
						"type":        "string",
						"description": "Folder ID (defaults to executor's folderID)",
					},
				},
			},
		},
	}, nil
}

// Execute runs a Yandex Cloud tool call.
func (y *YandexCloudExecutor) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	switch call.Name {
	case "yc_compute_list_instances":
		return y.computeListInstances(ctx, call)
	case "yc_compute_instance_details":
		return y.computeInstanceDetails(ctx, call)
	case "yc_monitoring_read":
		return y.monitoringRead(ctx, call)
	case "yc_managed_db_list":
		return y.managedDBList(ctx, call)
	case "yc_managed_db_hosts":
		return y.managedDBHosts(ctx, call)
	case "yc_vpc_list":
		return y.vpcList(ctx, call)
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

func (y *YandexCloudExecutor) computeListInstances(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	folderID := y.getFolderID(call.Input)

	url := fmt.Sprintf("https://compute.api.cloud.yandex.net/compute/v1/instances?folderId=%s", folderID)
	if filter, _ := call.Input["filter"].(string); filter != "" {
		url += "&filter=" + filter
	}

	body, err := y.doGet(ctx, url)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("Compute API error: %v", err), IsError: true}, nil
	}

	formatted, err := formatYCInstances(body)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("format error: %v\nRaw: %s", err, string(body)), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

func (y *YandexCloudExecutor) computeInstanceDetails(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	instanceID, _ := call.Input["instance_id"].(string)
	if instanceID == "" {
		return &domain.ToolResult{CallID: call.ID, Content: "missing required parameter: instance_id", IsError: true}, nil
	}

	url := fmt.Sprintf("https://compute.api.cloud.yandex.net/compute/v1/instances/%s", instanceID)

	body, err := y.doGet(ctx, url)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("Compute API error: %v", err), IsError: true}, nil
	}

	formatted, err := formatYCInstanceDetails(body)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("format error: %v\nRaw: %s", err, string(body)), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

func (y *YandexCloudExecutor) monitoringRead(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	query, _ := call.Input["query"].(string)
	fromStr, _ := call.Input["from"].(string)
	toStr, _ := call.Input["to"].(string)

	if query == "" || fromStr == "" || toStr == "" {
		return &domain.ToolResult{CallID: call.ID, Content: "missing required parameters: query, from, to", IsError: true}, nil
	}

	now := time.Now()
	fromTime, err := parseRelativeTime(fromStr, now)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("invalid 'from' time: %v", err), IsError: true}, nil
	}
	toTime, err := parseRelativeTime(toStr, now)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("invalid 'to' time: %v", err), IsError: true}, nil
	}

	folderID := y.getFolderID(call.Input)

	url := fmt.Sprintf("https://monitoring.api.cloud.yandex.net/monitoring/v2/data/read?folderId=%s", folderID)

	payload := map[string]interface{}{
		"query":      query,
		"fromTime":   fromTime.UTC().Format(time.RFC3339),
		"toTime":     toTime.UTC().Format(time.RFC3339),
		"downsampling": map[string]interface{}{
			"maxPoints": 100,
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("marshal payload: %v", err), IsError: true}, nil
	}

	body, err := y.doPost(ctx, url, payloadBytes)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("Monitoring API error: %v", err), IsError: true}, nil
	}

	formatted, err := formatYCMonitoring(body, query)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("format error: %v\nRaw: %s", err, string(body)), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

func (y *YandexCloudExecutor) managedDBList(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	service, _ := call.Input["service"].(string)
	if service == "" {
		return &domain.ToolResult{CallID: call.ID, Content: "missing required parameter: service", IsError: true}, nil
	}

	if !isValidMDBService(service) {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unsupported service %q: must be one of postgresql, mysql, redis, mongodb, clickhouse, kafka", service),
			IsError: true,
		}, nil
	}

	folderID := y.getFolderID(call.Input)
	url := fmt.Sprintf("https://mdb.api.cloud.yandex.net/managed-%s/v1/clusters?folderId=%s", service, folderID)

	body, err := y.doGet(ctx, url)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("MDB API error: %v", err), IsError: true}, nil
	}

	formatted, err := formatYCClusters(body, service)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("format error: %v\nRaw: %s", err, string(body)), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

func (y *YandexCloudExecutor) managedDBHosts(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	service, _ := call.Input["service"].(string)
	clusterID, _ := call.Input["cluster_id"].(string)

	if service == "" || clusterID == "" {
		return &domain.ToolResult{CallID: call.ID, Content: "missing required parameters: service, cluster_id", IsError: true}, nil
	}

	if !isValidMDBService(service) {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unsupported service %q: must be one of postgresql, mysql, redis, mongodb, clickhouse, kafka", service),
			IsError: true,
		}, nil
	}

	url := fmt.Sprintf("https://mdb.api.cloud.yandex.net/managed-%s/v1/clusters/%s/hosts", service, clusterID)

	body, err := y.doGet(ctx, url)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("MDB API error: %v", err), IsError: true}, nil
	}

	formatted, err := formatYCHosts(body, service)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("format error: %v\nRaw: %s", err, string(body)), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

func (y *YandexCloudExecutor) vpcList(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	folderID := y.getFolderID(call.Input)
	url := fmt.Sprintf("https://vpc.api.cloud.yandex.net/vpc/v1/networks?folderId=%s", folderID)

	body, err := y.doGet(ctx, url)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("VPC API error: %v", err), IsError: true}, nil
	}

	formatted, err := formatYCNetworks(body)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("format error: %v\nRaw: %s", err, string(body)), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

func (y *YandexCloudExecutor) doGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+y.token)

	resp, err := y.client.Do(req)
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

func (y *YandexCloudExecutor) doPost(ctx context.Context, url string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+y.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := y.client.Do(req)
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

func (y *YandexCloudExecutor) getFolderID(input map[string]interface{}) string {
	if fid, _ := input["folder_id"].(string); fid != "" {
		return fid
	}
	return y.folderID
}

func isValidMDBService(service string) bool {
	switch service {
	case "postgresql", "mysql", "redis", "mongodb", "clickhouse", "kafka":
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Response formatters
// ---------------------------------------------------------------------------

// ycInstance represents a Compute instance in the API response.
type ycInstance struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Status          string            `json:"status"`
	ZoneID          string            `json:"zoneId"`
	PlatformID      string            `json:"platformId"`
	Resources       *ycResources      `json:"resources"`
	NetworkIfaces   []ycNetworkIface  `json:"networkInterfaces"`
	Labels          map[string]string `json:"labels"`
	FQDN            string            `json:"fqdn"`
	CreatedAt       string            `json:"createdAt"`
	Description     string            `json:"description"`
	BootDisk        *ycAttachedDisk   `json:"bootDisk"`
}

type ycResources struct {
	Memory int64 `json:"memory"`
	Cores  int64 `json:"cores"`
	CoreFraction int64 `json:"coreFraction"`
	Gpus   int64 `json:"gpus"`
}

type ycNetworkIface struct {
	Index            string               `json:"index"`
	SubnetID         string               `json:"subnetId"`
	PrimaryV4Address *ycPrimaryAddress     `json:"primaryV4Address"`
	PrimaryV6Address *ycPrimaryAddress     `json:"primaryV6Address"`
}

type ycPrimaryAddress struct {
	Address        string              `json:"address"`
	OneToOneNat    *ycOneToOneNat      `json:"oneToOneNat"`
}

type ycOneToOneNat struct {
	Address   string `json:"address"`
	IPVersion string `json:"ipVersion"`
}

type ycAttachedDisk struct {
	DiskID     string `json:"diskId"`
	DeviceName string `json:"deviceName"`
	Mode       string `json:"mode"`
}

func formatYCInstances(body []byte) (string, error) {
	var resp struct {
		Instances []ycInstance `json:"instances"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal json: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Yandex Cloud Compute Instances: %d found\n\n", len(resp.Instances)))

	for _, inst := range resp.Instances {
		sb.WriteString(fmt.Sprintf("Instance: %s (%s)\n", inst.Name, inst.ID))
		sb.WriteString(fmt.Sprintf("  Status: %s\n", inst.Status))
		sb.WriteString(fmt.Sprintf("  Zone: %s\n", inst.ZoneID))
		sb.WriteString(fmt.Sprintf("  Platform: %s\n", inst.PlatformID))
		if inst.Resources != nil {
			sb.WriteString(fmt.Sprintf("  Resources: %d cores (%d%%), %d MB RAM",
				inst.Resources.Cores, inst.Resources.CoreFraction, inst.Resources.Memory/(1024*1024)))
			if inst.Resources.Gpus > 0 {
				sb.WriteString(fmt.Sprintf(", %d GPUs", inst.Resources.Gpus))
			}
			sb.WriteString("\n")
		}
		for _, iface := range inst.NetworkIfaces {
			if iface.PrimaryV4Address != nil {
				sb.WriteString(fmt.Sprintf("  Private IP: %s\n", iface.PrimaryV4Address.Address))
				if iface.PrimaryV4Address.OneToOneNat != nil {
					sb.WriteString(fmt.Sprintf("  Public IP: %s\n", iface.PrimaryV4Address.OneToOneNat.Address))
				}
			}
		}
		if inst.FQDN != "" {
			sb.WriteString(fmt.Sprintf("  FQDN: %s\n", inst.FQDN))
		}
		sb.WriteString("\n")
	}

	if len(resp.Instances) == 0 {
		sb.WriteString("  (no instances found)\n")
	}

	return sb.String(), nil
}

func formatYCInstanceDetails(body []byte) (string, error) {
	var inst ycInstance
	if err := json.Unmarshal(body, &inst); err != nil {
		return "", fmt.Errorf("unmarshal json: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Instance: %s (%s)\n", inst.Name, inst.ID))
	sb.WriteString(fmt.Sprintf("  Status: %s\n", inst.Status))
	sb.WriteString(fmt.Sprintf("  Zone: %s\n", inst.ZoneID))
	sb.WriteString(fmt.Sprintf("  Platform: %s\n", inst.PlatformID))
	if inst.Description != "" {
		sb.WriteString(fmt.Sprintf("  Description: %s\n", inst.Description))
	}
	sb.WriteString(fmt.Sprintf("  Created: %s\n", inst.CreatedAt))
	if inst.Resources != nil {
		sb.WriteString(fmt.Sprintf("  Resources: %d cores (%d%%), %d MB RAM",
			inst.Resources.Cores, inst.Resources.CoreFraction, inst.Resources.Memory/(1024*1024)))
		if inst.Resources.Gpus > 0 {
			sb.WriteString(fmt.Sprintf(", %d GPUs", inst.Resources.Gpus))
		}
		sb.WriteString("\n")
	}
	if inst.BootDisk != nil {
		sb.WriteString(fmt.Sprintf("  Boot disk: %s (mode: %s)\n", inst.BootDisk.DiskID, inst.BootDisk.Mode))
	}
	for _, iface := range inst.NetworkIfaces {
		sb.WriteString(fmt.Sprintf("  Network interface #%s (subnet: %s)\n", iface.Index, iface.SubnetID))
		if iface.PrimaryV4Address != nil {
			sb.WriteString(fmt.Sprintf("    Private IP: %s\n", iface.PrimaryV4Address.Address))
			if iface.PrimaryV4Address.OneToOneNat != nil {
				sb.WriteString(fmt.Sprintf("    Public IP: %s\n", iface.PrimaryV4Address.OneToOneNat.Address))
			}
		}
	}
	if len(inst.Labels) > 0 {
		sb.WriteString("  Labels:\n")
		for k, v := range inst.Labels {
			sb.WriteString(fmt.Sprintf("    %s: %s\n", k, v))
		}
	}

	return sb.String(), nil
}

// ycMonitoringResponse represents the Yandex Monitoring read response.
type ycMonitoringResponse struct {
	Metrics []ycMetric `json:"metrics"`
}

type ycMetric struct {
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels"`
	Type       string            `json:"type"`
	Timeseries ycTimeseries      `json:"timeseries"`
}

type ycTimeseries struct {
	Timestamps []int64   `json:"timestamps"`
	Values     []float64 `json:"doubleValues"`
}

func formatYCMonitoring(body []byte, query string) (string, error) {
	var resp ycMonitoringResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal json: %w", err)
	}

	var sb strings.Builder
	totalPoints := 0
	for _, m := range resp.Metrics {
		totalPoints += len(m.Timeseries.Timestamps)
	}

	sb.WriteString(fmt.Sprintf("Query: %s\nMetrics: %d, Total datapoints: %d\n\n", query, len(resp.Metrics), totalPoints))

	for _, m := range resp.Metrics {
		name := m.Name
		if name == "" {
			name = "(unnamed)"
		}
		sb.WriteString(fmt.Sprintf("Metric: %s (type: %s)\n", name, m.Type))
		if len(m.Labels) > 0 {
			labelParts := make([]string, 0, len(m.Labels))
			for k, v := range m.Labels {
				labelParts = append(labelParts, fmt.Sprintf("%s=%s", k, v))
			}
			sb.WriteString(fmt.Sprintf("  Labels: %s\n", strings.Join(labelParts, ", ")))
		}
		for i, ts := range m.Timeseries.Timestamps {
			t := time.Unix(ts/1000, (ts%1000)*int64(time.Millisecond)).UTC().Format(time.RFC3339)
			val := 0.0
			if i < len(m.Timeseries.Values) {
				val = m.Timeseries.Values[i]
			}
			sb.WriteString(fmt.Sprintf("  %s => %.4f\n", t, val))
		}
		sb.WriteString("\n")
	}

	if totalPoints == 0 {
		sb.WriteString("  (no datapoints in the requested time range)\n")
	}

	return sb.String(), nil
}

// ycCluster represents a Managed Database cluster.
type ycCluster struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	Health      string            `json:"health"`
	Environment string            `json:"environment"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	Config      json.RawMessage   `json:"config"`
	CreatedAt   string            `json:"createdAt"`
}

func formatYCClusters(body []byte, service string) (string, error) {
	var resp struct {
		Clusters []ycCluster `json:"clusters"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal json: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Managed %s Clusters: %d found\n\n", service, len(resp.Clusters)))

	for _, c := range resp.Clusters {
		sb.WriteString(fmt.Sprintf("Cluster: %s (%s)\n", c.Name, c.ID))
		sb.WriteString(fmt.Sprintf("  Status: %s\n", c.Status))
		sb.WriteString(fmt.Sprintf("  Health: %s\n", c.Health))
		sb.WriteString(fmt.Sprintf("  Environment: %s\n", c.Environment))
		if c.Description != "" {
			sb.WriteString(fmt.Sprintf("  Description: %s\n", c.Description))
		}
		sb.WriteString(fmt.Sprintf("  Created: %s\n", c.CreatedAt))
		if len(c.Labels) > 0 {
			sb.WriteString("  Labels:\n")
			for k, v := range c.Labels {
				sb.WriteString(fmt.Sprintf("    %s: %s\n", k, v))
			}
		}
		sb.WriteString("\n")
	}

	if len(resp.Clusters) == 0 {
		sb.WriteString("  (no clusters found)\n")
	}

	return sb.String(), nil
}

// ycHost represents a Managed Database host.
type ycHost struct {
	Name            string `json:"name"`
	ClusterID       string `json:"clusterId"`
	ZoneID          string `json:"zoneId"`
	Role            string `json:"role"`
	Health          string `json:"health"`
	SubnetID        string `json:"subnetId"`
	ReplicaType     string `json:"replicaType"`
	AssignPublicIP  bool   `json:"assignPublicIp"`
}

func formatYCHosts(body []byte, service string) (string, error) {
	var resp struct {
		Hosts []ycHost `json:"hosts"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal json: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Managed %s Hosts: %d found\n\n", service, len(resp.Hosts)))

	for _, h := range resp.Hosts {
		sb.WriteString(fmt.Sprintf("Host: %s\n", h.Name))
		sb.WriteString(fmt.Sprintf("  Role: %s\n", h.Role))
		sb.WriteString(fmt.Sprintf("  Health: %s\n", h.Health))
		sb.WriteString(fmt.Sprintf("  Zone: %s\n", h.ZoneID))
		sb.WriteString(fmt.Sprintf("  Subnet: %s\n", h.SubnetID))
		if h.AssignPublicIP {
			sb.WriteString("  Public IP: assigned\n")
		}
		if h.ReplicaType != "" {
			sb.WriteString(fmt.Sprintf("  Replica type: %s\n", h.ReplicaType))
		}
		sb.WriteString("\n")
	}

	if len(resp.Hosts) == 0 {
		sb.WriteString("  (no hosts found)\n")
	}

	return sb.String(), nil
}

// ycNetwork represents a VPC network.
type ycNetwork struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	FolderID    string `json:"folderId"`
	CreatedAt   string `json:"createdAt"`
}

func formatYCNetworks(body []byte) (string, error) {
	var resp struct {
		Networks []ycNetwork `json:"networks"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal json: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("VPC Networks: %d found\n\n", len(resp.Networks)))

	for _, n := range resp.Networks {
		sb.WriteString(fmt.Sprintf("Network: %s (%s)\n", n.Name, n.ID))
		if n.Description != "" {
			sb.WriteString(fmt.Sprintf("  Description: %s\n", n.Description))
		}
		sb.WriteString(fmt.Sprintf("  Folder: %s\n", n.FolderID))
		sb.WriteString(fmt.Sprintf("  Created: %s\n", n.CreatedAt))
		sb.WriteString("\n")
	}

	if len(resp.Networks) == 0 {
		sb.WriteString("  (no networks found)\n")
	}

	return sb.String(), nil
}
