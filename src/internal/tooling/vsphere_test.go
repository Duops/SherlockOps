package tooling

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// newTestVSphereServer creates a fake vCenter REST API server.
func newTestVSphereServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Auth endpoint.
		if r.URL.Path == "/api/session" && r.Method == http.MethodPost {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "admin" || pass != "secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode("test-session-token-abc123")
			return
		}

		// Validate session token for all other requests.
		token := r.Header.Get("vmware-api-session-id")
		if token != "test-session-token-abc123" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		switch {
		case r.URL.Path == "/api/vcenter/vm" && r.Method == http.MethodGet:
			vms := []vsphereVMSummary{
				{
					VM:         "vm-101",
					Name:       "web-server-01",
					PowerState: "POWERED_ON",
					CPUCount:   4,
					MemoryMB:   8192,
					GuestOS:    "UBUNTU_64",
				},
				{
					VM:         "vm-102",
					Name:       "db-server-01",
					PowerState: "POWERED_ON",
					CPUCount:   8,
					MemoryMB:   32768,
					GuestOS:    "RHEL_8_64",
				},
			}

			// Apply name filter if present.
			if names, ok := r.URL.Query()["filter.names"]; ok && len(names) > 0 {
				nameSet := map[string]bool{}
				for _, n := range names {
					nameSet[n] = true
				}
				var filtered []vsphereVMSummary
				for _, vm := range vms {
					if nameSet[vm.Name] {
						filtered = append(filtered, vm)
					}
				}
				vms = filtered
			}

			// Apply power state filter if present.
			if states, ok := r.URL.Query()["filter.power_states"]; ok && len(states) > 0 {
				stateSet := map[string]bool{}
				for _, s := range states {
					stateSet[s] = true
				}
				var filtered []vsphereVMSummary
				for _, vm := range vms {
					if stateSet[vm.PowerState] {
						filtered = append(filtered, vm)
					}
				}
				vms = filtered
			}

			json.NewEncoder(w).Encode(vms)

		case strings.HasPrefix(r.URL.Path, "/api/vcenter/vm/") && r.Method == http.MethodGet:
			vmID := strings.TrimPrefix(r.URL.Path, "/api/vcenter/vm/")
			if vmID != "vm-101" {
				http.Error(w, `{"error_type":"NOT_FOUND"}`, http.StatusNotFound)
				return
			}
			bootTime := "2024-01-15T08:30:00Z"
			detail := vsphereVMDetail{
				Name:       "web-server-01",
				GuestOS:    "UBUNTU_64",
				PowerState: "POWERED_ON",
				BootTime:   &bootTime,
				CPU: vsphereVMCPU{
					Count:          4,
					CoresPerSocket: 2,
					HotAddEnabled:  true,
				},
				Memory: vsphereVMMemory{
					SizeMB:        8192,
					HotAddEnabled: false,
				},
				Disks: map[string]vsphereDisk{
					"2000": {
						Label:    "Hard disk 1",
						Type:     "SCSI",
						Capacity: 107374182400, // 100 GB
						Backing: vsphereBackingInfo{
							Type:     "VMDK_FILE",
							VMDKFile: "[datastore1] web-server-01/web-server-01.vmdk",
						},
					},
				},
				NICs: map[string]vsphereNIC{
					"4000": {
						Label:      "Network adapter 1",
						Type:       "VMXNET3",
						MACAddress: "00:50:56:ab:cd:ef",
						MACType:    "ASSIGNED",
						State:      "CONNECTED",
					},
				},
				Guest: &vsphereGuest{
					OSFullName: "Ubuntu 22.04.3 LTS",
					HostName:   "web-server-01",
					IPAddress:  "10.0.1.50",
				},
			}
			json.NewEncoder(w).Encode(detail)

		case r.URL.Path == "/api/vcenter/host" && r.Method == http.MethodGet:
			hosts := []vsphereHostSummary{
				{
					Host:            "host-10",
					Name:            "esxi-01.example.com",
					ConnectionState: "CONNECTED",
					PowerState:      "POWERED_ON",
				},
				{
					Host:            "host-11",
					Name:            "esxi-02.example.com",
					ConnectionState: "CONNECTED",
					PowerState:      "POWERED_ON",
				},
			}
			json.NewEncoder(w).Encode(hosts)

		case r.URL.Path == "/api/vcenter/datastore" && r.Method == http.MethodGet:
			datastores := []vsphereDatastoreSummary{
				{
					Datastore: "datastore-20",
					Name:      "datastore1",
					Type:      "VMFS",
					Capacity:  1099511627776, // 1 TB
					FreeSpace: 549755813888,  // 512 GB
				},
				{
					Datastore: "datastore-21",
					Name:      "nfs-share",
					Type:      "NFS",
					Capacity:  2199023255552, // 2 TB
					FreeSpace: 219902325555,  // ~205 GB
				},
			}
			json.NewEncoder(w).Encode(datastores)

		case r.URL.Path == "/api/cis/tasks" && r.Method == http.MethodGet:
			tasks := []vsphereTask{
				{
					Task:        "task-1",
					Description: "VM snapshot creation",
					Status:      "RUNNING",
					Target:      "vm-101",
					StartTime:   "2024-01-15T10:00:00Z",
				},
			}
			json.NewEncoder(w).Encode(tasks)

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func newTestVSphereExecutor(t *testing.T, serverURL string) *VSphereExecutor {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return NewVSphereExecutor(serverURL, "admin", "secret", false, logger)
}

func TestVSphereExecutor_ListTools(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewVSphereExecutor("https://vcenter.example.com", "admin", "pass", true, logger)

	tools, err := exec.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if len(tools) != 5 {
		t.Fatalf("expected 5 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	expected := []string{
		"vsphere_list_vms", "vsphere_vm_details", "vsphere_host_list",
		"vsphere_datastore_list", "vsphere_alarms",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestVSphereExecutor_Auth(t *testing.T) {
	srv := newTestVSphereServer(t)
	defer srv.Close()

	exec := newTestVSphereExecutor(t, srv.URL)

	// Token should be empty before first call.
	if exec.token != "" {
		t.Error("expected empty token before auth")
	}

	// Execute a call which triggers authentication.
	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-auth",
		Name:  "vsphere_host_list",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	// Token should be set now.
	if exec.token != "test-session-token-abc123" {
		t.Errorf("expected token to be set, got: %s", exec.token)
	}
}

func TestVSphereExecutor_AuthReauthOn401(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/api/session" && r.Method == http.MethodPost {
			json.NewEncoder(w).Encode("fresh-token")
			return
		}

		token := r.Header.Get("vmware-api-session-id")
		callCount++
		// First data call returns 401 to force re-auth; second succeeds.
		if callCount == 1 && token == "expired-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		json.NewEncoder(w).Encode([]vsphereHostSummary{
			{Host: "host-1", Name: "esxi-01", ConnectionState: "CONNECTED", PowerState: "POWERED_ON"},
		})
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewVSphereExecutor(srv.URL, "admin", "secret", false, logger)
	exec.token = "expired-token" // Simulate expired session.

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-reauth",
		Name:  "vsphere_host_list",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "esxi-01") {
		t.Errorf("expected host info in result, got: %s", result.Content)
	}
}

func TestVSphereExecutor_ListVMs(t *testing.T) {
	srv := newTestVSphereServer(t)
	defer srv.Close()

	exec := newTestVSphereExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-vms",
		Name:  "vsphere_list_vms",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "web-server-01") {
		t.Errorf("expected web-server-01, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "db-server-01") {
		t.Errorf("expected db-server-01, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "POWERED_ON") {
		t.Errorf("expected POWERED_ON, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "2 found") {
		t.Errorf("expected 2 found count, got: %s", result.Content)
	}
}

func TestVSphereExecutor_ListVMsWithFilter(t *testing.T) {
	srv := newTestVSphereServer(t)
	defer srv.Close()

	exec := newTestVSphereExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-vms-filtered",
		Name: "vsphere_list_vms",
		Input: map[string]interface{}{
			"filter_power_states": []interface{}{"POWERED_OFF"},
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// All test VMs are POWERED_ON, so filter for POWERED_OFF should return none.
	if !strings.Contains(result.Content, "No virtual machines found") {
		t.Errorf("expected no VMs for POWERED_OFF filter, got: %s", result.Content)
	}
}

func TestVSphereExecutor_VMDetails(t *testing.T) {
	srv := newTestVSphereServer(t)
	defer srv.Close()

	exec := newTestVSphereExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-vm-detail",
		Name: "vsphere_vm_details",
		Input: map[string]interface{}{
			"vm_id": "vm-101",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	checks := []string{
		"web-server-01",
		"POWERED_ON",
		"4 vCPUs",
		"8192 MB",
		"Hard disk 1",
		"100.0 GB",
		"Network adapter 1",
		"VMXNET3",
		"00:50:56:ab:cd:ef",
		"Ubuntu 22.04.3 LTS",
		"10.0.1.50",
		"Boot Time",
	}
	for _, check := range checks {
		if !strings.Contains(result.Content, check) {
			t.Errorf("expected %q in VM details, got: %s", check, result.Content)
		}
	}
}

func TestVSphereExecutor_VMDetailsMissingParam(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewVSphereExecutor("https://vcenter.example.com", "admin", "pass", true, logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-vm-noparam",
		Name:  "vsphere_vm_details",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing vm_id parameter")
	}
	if !strings.Contains(result.Content, "missing required parameter: vm_id") {
		t.Errorf("expected missing param message, got: %s", result.Content)
	}
}

func TestVSphereExecutor_ListVMsWithNameFilter(t *testing.T) {
	srv := newTestVSphereServer(t)
	defer srv.Close()

	exec := newTestVSphereExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-vms-name-filter",
		Name: "vsphere_list_vms",
		Input: map[string]interface{}{
			"filter_names": []interface{}{"web-server-01"},
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "web-server-01") {
		t.Errorf("expected web-server-01 in result, got: %s", result.Content)
	}
	if strings.Contains(result.Content, "db-server-01") {
		t.Errorf("expected db-server-01 to be filtered out, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "1 found") {
		t.Errorf("expected 1 found, got: %s", result.Content)
	}
}

func TestVSphereExecutor_AuthFailure(t *testing.T) {
	srv := newTestVSphereServer(t)
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewVSphereExecutor(srv.URL, "admin", "wrong-password", false, logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-auth-fail",
		Name:  "vsphere_host_list",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for failed auth")
	}
	if !strings.Contains(result.Content, "auth failed") && !strings.Contains(result.Content, "error") {
		t.Errorf("expected auth failure message, got: %s", result.Content)
	}
}

func TestVSphereExecutor_ConnectionError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewVSphereExecutor("http://127.0.0.1:1", "admin", "pass", false, logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-conn-err",
		Name:  "vsphere_host_list",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for connection failure")
	}
}

func TestVSphereExecutor_HostList(t *testing.T) {
	srv := newTestVSphereServer(t)
	defer srv.Close()

	exec := newTestVSphereExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-hosts",
		Name:  "vsphere_host_list",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "esxi-01.example.com") {
		t.Errorf("expected esxi-01, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "esxi-02.example.com") {
		t.Errorf("expected esxi-02, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "CONNECTED") {
		t.Errorf("expected CONNECTED, got: %s", result.Content)
	}
}

func TestVSphereExecutor_DatastoreList(t *testing.T) {
	srv := newTestVSphereServer(t)
	defer srv.Close()

	exec := newTestVSphereExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-ds",
		Name:  "vsphere_datastore_list",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "datastore1") {
		t.Errorf("expected datastore1, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "VMFS") {
		t.Errorf("expected VMFS type, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "50.0%") {
		t.Errorf("expected 50%% usage for datastore1, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "nfs-share") {
		t.Errorf("expected nfs-share, got: %s", result.Content)
	}
}

func TestVSphereExecutor_Alarms(t *testing.T) {
	srv := newTestVSphereServer(t)
	defer srv.Close()

	exec := newTestVSphereExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-alarms",
		Name:  "vsphere_alarms",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "VM snapshot creation") {
		t.Errorf("expected alarm description, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "RUNNING") {
		t.Errorf("expected RUNNING status, got: %s", result.Content)
	}
}

func TestVSphereExecutor_UnknownTool(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewVSphereExecutor("https://vcenter.example.com", "admin", "pass", true, logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-unknown",
		Name: "vsphere_nonexistent",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unknown tool")
	}
}
