package tooling

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

// VSphereExecutor provides VMware vSphere REST API tools for alert analysis.
type VSphereExecutor struct {
	url      string // vCenter URL (e.g., https://vcenter.example.com)
	username string
	password string
	insecure bool // skip TLS verify (common in on-prem)
	client   *http.Client
	token    string // session token after login
	mu       sync.Mutex
	logger   *slog.Logger
}

// NewVSphereExecutor creates a new vSphere tool executor.
func NewVSphereExecutor(url, username, password string, insecure bool, logger *slog.Logger) *VSphereExecutor {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // user-configured
	}

	return &VSphereExecutor{
		url:      strings.TrimRight(url, "/"),
		username: username,
		password: password,
		insecure: insecure,
		client:   &http.Client{Timeout: 30 * time.Second, Transport: transport},
		logger:   logger,
	}
}

// ListTools returns the available vSphere tools.
func (v *VSphereExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	return []domain.Tool{
		{
			Name:        "vsphere_list_vms",
			Description: "List virtual machines in vSphere/vCenter with name, power state, CPU, and memory information.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filter_names": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Optional list of VM names to filter by (e.g. [\"web-01\"])",
					},
					"filter_power_states": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string", "enum": []string{"POWERED_ON", "POWERED_OFF", "SUSPENDED"}},
						"description": "Optional list of power states to filter by",
					},
				},
			},
		},
		{
			Name:        "vsphere_vm_details",
			Description: "Get detailed configuration for a specific VM including CPU, memory, disks, NICs, guest OS, and boot time.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"vm_id": map[string]interface{}{
						"type":        "string",
						"description": "VM identifier (e.g., vm-123)",
					},
				},
				"required": []interface{}{"vm_id"},
			},
		},
		{
			Name:        "vsphere_host_list",
			Description: "List ESXi hosts in vCenter with connection and power state.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "vsphere_datastore_list",
			Description: "List datastores in vCenter with capacity, free space, and usage percentage.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "vsphere_alarms",
			Description: "Get active/triggered alarms from vCenter with severity information.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}, nil
}

// Execute runs a vSphere tool call.
func (v *VSphereExecutor) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	switch call.Name {
	case "vsphere_list_vms":
		return v.listVMs(ctx, call)
	case "vsphere_vm_details":
		return v.vmDetails(ctx, call)
	case "vsphere_host_list":
		return v.hostList(ctx, call)
	case "vsphere_datastore_list":
		return v.datastoreList(ctx, call)
	case "vsphere_alarms":
		return v.alarms(ctx, call)
	default:
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unknown tool: %s", call.Name),
			IsError: true,
		}, nil
	}
}

// authenticate creates a session with vCenter using Basic Auth.
func (v *VSphereExecutor) authenticate(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.url+"/api/session", nil)
	if err != nil {
		return fmt.Errorf("create auth request: %w", err)
	}
	req.SetBasicAuth(v.username, v.password)

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("auth request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("auth failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	// The vSphere REST API returns the token as a JSON string (quoted).
	var token string
	if err := json.Unmarshal(body, &token); err != nil {
		// Some versions return bare string without quotes.
		token = strings.Trim(string(body), "\" \n\r")
	}

	if token == "" {
		return fmt.Errorf("empty session token received")
	}

	v.token = token
	v.logger.Debug("vSphere authenticated", "url", v.url)
	return nil
}

// doGet performs an authenticated GET request, re-authenticating on 401.
func (v *VSphereExecutor) doGet(ctx context.Context, path string) ([]byte, error) {
	// Ensure we have a token.
	if v.token == "" {
		if err := v.authenticate(ctx); err != nil {
			return nil, err
		}
	}

	body, statusCode, err := v.doGetOnce(ctx, path)
	if err != nil {
		return nil, err
	}

	// Re-authenticate on 401 and retry once.
	if statusCode == http.StatusUnauthorized {
		v.logger.Debug("vSphere session expired, re-authenticating")
		if err := v.authenticate(ctx); err != nil {
			return nil, fmt.Errorf("re-auth: %w", err)
		}
		body, statusCode, err = v.doGetOnce(ctx, path)
		if err != nil {
			return nil, err
		}
		if statusCode != http.StatusOK {
			return nil, fmt.Errorf("http %d after re-auth: %s", statusCode, string(body))
		}
	}

	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d: %s", statusCode, string(body))
	}

	return body, nil
}

func (v *VSphereExecutor) doGetOnce(ctx context.Context, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.url+path, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("vmware-api-session-id", v.token)

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read body: %w", err)
	}

	return body, resp.StatusCode, nil
}

// --- VM List ---

type vsphereVMSummary struct {
	VM         string `json:"vm"`
	Name       string `json:"name"`
	PowerState string `json:"power_state"`
	CPUCount   int    `json:"cpu_count"`
	MemoryMB   int64  `json:"memory_size_MiB"`
	GuestOS    string `json:"guest_OS,omitempty"`
}

func (v *VSphereExecutor) listVMs(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	path := "/api/vcenter/vm"
	var params []string

	if names, ok := call.Input["filter_names"]; ok {
		for _, n := range toStringSlice(names) {
			params = append(params, "filter.names="+n)
		}
	}
	if states, ok := call.Input["filter_power_states"]; ok {
		for _, s := range toStringSlice(states) {
			params = append(params, "filter.power_states="+s)
		}
	}
	if len(params) > 0 {
		path += "?" + strings.Join(params, "&")
	}

	body, err := v.doGet(ctx, path)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("vSphere list VMs error: %v", err),
			IsError: true,
		}, nil
	}

	var vms []vsphereVMSummary
	if err := json.Unmarshal(body, &vms); err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("parse error: %v\nRaw: %s", err, string(body)),
			IsError: true,
		}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatVMList(vms)}, nil
}

func formatVMList(vms []vsphereVMSummary) string {
	if len(vms) == 0 {
		return "No virtual machines found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Virtual Machines (%d found):\n\n", len(vms)))
	for _, vm := range vms {
		sb.WriteString(fmt.Sprintf("  %s (ID: %s)\n", vm.Name, vm.VM))
		sb.WriteString(fmt.Sprintf("    Power State: %s\n", vm.PowerState))
		sb.WriteString(fmt.Sprintf("    CPU: %d vCPUs, Memory: %d MB\n", vm.CPUCount, vm.MemoryMB))
		if vm.GuestOS != "" {
			sb.WriteString(fmt.Sprintf("    Guest OS: %s\n", vm.GuestOS))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// --- VM Details ---

type vsphereVMDetail struct {
	Name       string               `json:"name"`
	GuestOS    string               `json:"guest_OS"`
	PowerState string               `json:"power_state"`
	CPU        vsphereVMCPU         `json:"cpu"`
	Memory     vsphereVMMemory      `json:"memory"`
	Disks      map[string]vsphereDisk `json:"disks"`
	NICs       map[string]vsphereNIC  `json:"nics"`
	BootTime   *string              `json:"boot_time,omitempty"`
	Guest      *vsphereGuest        `json:"guest,omitempty"`
}

type vsphereVMCPU struct {
	Count          int  `json:"count"`
	CoresPerSocket int  `json:"cores_per_socket"`
	HotAddEnabled  bool `json:"hot_add_enabled"`
}

type vsphereVMMemory struct {
	SizeMB        int64 `json:"size_MiB"`
	HotAddEnabled bool  `json:"hot_add_enabled"`
}

type vsphereDisk struct {
	Label    string          `json:"label"`
	Type     string          `json:"type"`
	Capacity int64           `json:"capacity"`
	Backing  vsphereBackingInfo `json:"backing"`
}

type vsphereBackingInfo struct {
	Type     string `json:"type"`
	VMDKFile string `json:"vmdk_file,omitempty"`
}

type vsphereNIC struct {
	Label         string `json:"label"`
	Type          string `json:"type"`
	MACAddress    string `json:"mac_address"`
	MACType       string `json:"mac_type"`
	State         string `json:"state"`
	BackingType   string `json:"backing_type,omitempty"`
}

type vsphereGuest struct {
	OSFullName string `json:"os_full_name,omitempty"`
	HostName   string `json:"host_name,omitempty"`
	IPAddress  string `json:"ip_address,omitempty"`
}

func (v *VSphereExecutor) vmDetails(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	vmID, _ := call.Input["vm_id"].(string)
	if vmID == "" {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: "missing required parameter: vm_id",
			IsError: true,
		}, nil
	}

	// Validate vmID to prevent path traversal (must be alphanumeric with hyphens).
	for _, c := range vmID {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
			return &domain.ToolResult{
				CallID:  call.ID,
				Content: "invalid vm_id: must contain only alphanumeric characters and hyphens",
				IsError: true,
			}, nil
		}
	}

	body, err := v.doGet(ctx, "/api/vcenter/vm/"+vmID)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("vSphere VM details error: %v", err),
			IsError: true,
		}, nil
	}

	var detail vsphereVMDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("parse error: %v\nRaw: %s", err, string(body)),
			IsError: true,
		}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatVMDetail(detail)}, nil
}

func formatVMDetail(d vsphereVMDetail) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("VM: %s\n", d.Name))
	sb.WriteString(fmt.Sprintf("  Power State: %s\n", d.PowerState))
	sb.WriteString(fmt.Sprintf("  Guest OS: %s\n", d.GuestOS))
	if d.BootTime != nil {
		sb.WriteString(fmt.Sprintf("  Boot Time: %s\n", *d.BootTime))
	}

	sb.WriteString(fmt.Sprintf("\n  CPU: %d vCPUs (%d cores/socket, hot-add: %v)\n",
		d.CPU.Count, d.CPU.CoresPerSocket, d.CPU.HotAddEnabled))
	sb.WriteString(fmt.Sprintf("  Memory: %d MB (hot-add: %v)\n",
		d.Memory.SizeMB, d.Memory.HotAddEnabled))

	if len(d.Disks) > 0 {
		sb.WriteString("\n  Disks:\n")
		for key, disk := range d.Disks {
			capacityGB := float64(disk.Capacity) / (1024 * 1024 * 1024)
			sb.WriteString(fmt.Sprintf("    [%s] %s - Type: %s, Capacity: %.1f GB\n",
				key, disk.Label, disk.Type, capacityGB))
			if disk.Backing.VMDKFile != "" {
				sb.WriteString(fmt.Sprintf("      Backing: %s\n", disk.Backing.VMDKFile))
			}
		}
	}

	if len(d.NICs) > 0 {
		sb.WriteString("\n  Network Adapters:\n")
		for key, nic := range d.NICs {
			sb.WriteString(fmt.Sprintf("    [%s] %s - Type: %s, MAC: %s, State: %s\n",
				key, nic.Label, nic.Type, nic.MACAddress, nic.State))
		}
	}

	if d.Guest != nil {
		sb.WriteString("\n  Guest Info:\n")
		if d.Guest.OSFullName != "" {
			sb.WriteString(fmt.Sprintf("    OS: %s\n", d.Guest.OSFullName))
		}
		if d.Guest.HostName != "" {
			sb.WriteString(fmt.Sprintf("    Hostname: %s\n", d.Guest.HostName))
		}
		if d.Guest.IPAddress != "" {
			sb.WriteString(fmt.Sprintf("    IP: %s\n", d.Guest.IPAddress))
		}
	}

	return sb.String()
}

// --- Host List ---

type vsphereHostSummary struct {
	Host            string `json:"host"`
	Name            string `json:"name"`
	ConnectionState string `json:"connection_state"`
	PowerState      string `json:"power_state"`
}

func (v *VSphereExecutor) hostList(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	body, err := v.doGet(ctx, "/api/vcenter/host")
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("vSphere host list error: %v", err),
			IsError: true,
		}, nil
	}

	var hosts []vsphereHostSummary
	if err := json.Unmarshal(body, &hosts); err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("parse error: %v\nRaw: %s", err, string(body)),
			IsError: true,
		}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatHostList(hosts)}, nil
}

func formatHostList(hosts []vsphereHostSummary) string {
	if len(hosts) == 0 {
		return "No ESXi hosts found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("ESXi Hosts (%d found):\n\n", len(hosts)))
	for _, h := range hosts {
		sb.WriteString(fmt.Sprintf("  %s (ID: %s)\n", h.Name, h.Host))
		sb.WriteString(fmt.Sprintf("    Connection: %s, Power: %s\n\n", h.ConnectionState, h.PowerState))
	}
	return sb.String()
}

// --- Datastore List ---

type vsphereDatastoreSummary struct {
	Datastore string `json:"datastore"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Capacity  int64  `json:"capacity"`
	FreeSpace int64  `json:"free_space"`
}

func (v *VSphereExecutor) datastoreList(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	body, err := v.doGet(ctx, "/api/vcenter/datastore")
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("vSphere datastore list error: %v", err),
			IsError: true,
		}, nil
	}

	var datastores []vsphereDatastoreSummary
	if err := json.Unmarshal(body, &datastores); err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("parse error: %v\nRaw: %s", err, string(body)),
			IsError: true,
		}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatDatastoreList(datastores)}, nil
}

func formatDatastoreList(datastores []vsphereDatastoreSummary) string {
	if len(datastores) == 0 {
		return "No datastores found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Datastores (%d found):\n\n", len(datastores)))
	for _, ds := range datastores {
		capacityGB := float64(ds.Capacity) / (1024 * 1024 * 1024)
		freeGB := float64(ds.FreeSpace) / (1024 * 1024 * 1024)
		usedPct := 0.0
		if ds.Capacity > 0 {
			usedPct = float64(ds.Capacity-ds.FreeSpace) / float64(ds.Capacity) * 100
		}
		sb.WriteString(fmt.Sprintf("  %s (ID: %s)\n", ds.Name, ds.Datastore))
		sb.WriteString(fmt.Sprintf("    Type: %s\n", ds.Type))
		sb.WriteString(fmt.Sprintf("    Capacity: %.1f GB, Free: %.1f GB, Used: %.1f%%\n\n",
			capacityGB, freeGB, usedPct))
	}
	return sb.String()
}

// --- Alarms ---

type vsphereTask struct {
	Task        string `json:"task"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	Target      string `json:"target,omitempty"`
	StartTime   string `json:"start_time,omitempty"`
}

// vsphereAlarm represents an alarm from the vCenter alarms API.
type vsphereAlarm struct {
	Alarm       string `json:"alarm"`
	Name        string `json:"name,omitempty"`
	Entity      string `json:"entity,omitempty"`
	EntityType  string `json:"entity_type,omitempty"`
	Status      string `json:"status,omitempty"`       // e.g., RED, YELLOW, GREEN
	Time        string `json:"time,omitempty"`
	Description string `json:"description,omitempty"`
	Acknowledged bool  `json:"acknowledged,omitempty"`
}

func (v *VSphereExecutor) alarms(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	// Try the triggered alarms endpoint first (vSphere 7.0.2+).
	body, err := v.doGet(ctx, "/api/cis/tasks")
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("vSphere alarms error: %v", err),
			IsError: true,
		}, nil
	}

	// Parse as task list (CIS tasks API).
	var tasks []vsphereTask
	if err := json.Unmarshal(body, &tasks); err != nil {
		// Might be a map keyed by task ID.
		var taskMap map[string]vsphereTask
		if err2 := json.Unmarshal(body, &taskMap); err2 != nil {
			return &domain.ToolResult{
				CallID:  call.ID,
				Content: fmt.Sprintf("parse error: %v\nRaw: %s", err, string(body)),
				IsError: true,
			}, nil
		}
		for id, t := range taskMap {
			t.Task = id
			tasks = append(tasks, t)
		}
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatTasks(tasks)}, nil
}

func formatTasks(tasks []vsphereTask) string {
	if len(tasks) == 0 {
		return "No active tasks or alarms found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Tasks/Alarms (%d found):\n\n", len(tasks)))
	for _, t := range tasks {
		sb.WriteString(fmt.Sprintf("  Task: %s\n", t.Task))
		if t.Description != "" {
			sb.WriteString(fmt.Sprintf("    Description: %s\n", t.Description))
		}
		sb.WriteString(fmt.Sprintf("    Status: %s\n", t.Status))
		if t.Target != "" {
			sb.WriteString(fmt.Sprintf("    Target: %s\n", t.Target))
		}
		if t.StartTime != "" {
			sb.WriteString(fmt.Sprintf("    Started: %s\n", t.StartTime))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// toStringSlice converts an interface{} (typically []interface{} from JSON) to []string.
func toStringSlice(v interface{}) []string {
	switch val := v.(type) {
	case []interface{}:
		result := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok && s != "" {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return val
	default:
		return nil
	}
}
