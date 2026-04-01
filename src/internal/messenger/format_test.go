package messenger

import (
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

func TestExtractTarget_Priority(t *testing.T) {
	tests := []struct {
		name       string
		labels     map[string]string
		wantType   string
		wantName   string
	}{
		{
			name:     "pod has highest priority",
			labels:   map[string]string{"pod": "nginx-abc", "instance": "10.0.0.1", "host": "node-1"},
			wantType: "pod",
			wantName: "nginx-abc",
		},
		{
			name:     "instance when no pod",
			labels:   map[string]string{"instance": "10.0.0.1", "host": "node-1"},
			wantType: "instance",
			wantName: "10.0.0.1",
		},
		{
			name:     "host when no pod or instance",
			labels:   map[string]string{"host": "node-1", "container": "app"},
			wantType: "host",
			wantName: "node-1",
		},
		{
			name:     "container",
			labels:   map[string]string{"container": "app", "service": "web"},
			wantType: "container",
			wantName: "app",
		},
		{
			name:     "service",
			labels:   map[string]string{"service": "web", "deployment": "deploy-1"},
			wantType: "service",
			wantName: "web",
		},
		{
			name:     "deployment",
			labels:   map[string]string{"deployment": "deploy-1", "job": "scraper"},
			wantType: "deployment",
			wantName: "deploy-1",
		},
		{
			name:     "job is lowest priority",
			labels:   map[string]string{"job": "scraper"},
			wantType: "job",
			wantName: "scraper",
		},
		{
			name:     "empty labels returns empty",
			labels:   map[string]string{},
			wantType: "",
			wantName: "",
		},
		{
			name:     "nil labels returns empty",
			labels:   nil,
			wantType: "",
			wantName: "",
		},
		{
			name:     "empty value is skipped",
			labels:   map[string]string{"pod": "", "instance": "10.0.0.1"},
			wantType: "instance",
			wantName: "10.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alert := &domain.Alert{Labels: tt.labels}
			gotType, gotName := extractTarget(alert)
			if gotType != tt.wantType {
				t.Errorf("extractTarget() type = %q, want %q", gotType, tt.wantType)
			}
			if gotName != tt.wantName {
				t.Errorf("extractTarget() name = %q, want %q", gotName, tt.wantName)
			}
		})
	}
}

func TestSeverityColor(t *testing.T) {
	tests := []struct {
		name   string
		sev    domain.Severity
		status domain.AlertStatus
		want   string
	}{
		{"resolved is green", domain.SeverityCritical, domain.StatusResolved, "#2EB67D"},
		{"critical is red", domain.SeverityCritical, domain.StatusFiring, "#E01E5A"},
		{"warning is yellow", domain.SeverityWarning, domain.StatusFiring, "#ECB22E"},
		{"info is blue", domain.SeverityInfo, domain.StatusFiring, "#36C5F0"},
		{"unknown severity is blue", domain.Severity("unknown"), domain.StatusFiring, "#36C5F0"},
		{"empty severity is blue", domain.Severity(""), domain.StatusFiring, "#36C5F0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := severityColor(tt.sev, tt.status)
			if got != tt.want {
				t.Errorf("severityColor(%q, %q) = %q, want %q", tt.sev, tt.status, got, tt.want)
			}
		})
	}
}

func TestAlertSeverityEmoji(t *testing.T) {
	tests := []struct {
		name   string
		sev    domain.Severity
		status domain.AlertStatus
		want   string
	}{
		{"resolved is green square", domain.SeverityCritical, domain.StatusResolved, "\U0001F7E9"},
		{"critical is red square", domain.SeverityCritical, domain.StatusFiring, "\U0001F7E5"},
		{"warning is yellow square", domain.SeverityWarning, domain.StatusFiring, "\U0001F7E8"},
		{"info is blue square", domain.SeverityInfo, domain.StatusFiring, "\U0001F7E6"},
		{"unknown is blue square", domain.Severity("other"), domain.StatusFiring, "\U0001F7E6"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := alertSeverityEmoji(tt.sev, tt.status)
			if got != tt.want {
				t.Errorf("alertSeverityEmoji(%q, %q) = %q, want %q", tt.sev, tt.status, got, tt.want)
			}
		})
	}
}

func TestFormatLabelsContext(t *testing.T) {
	tests := []struct {
		name        string
		labels      map[string]string
		excludeKeys []string
		want        string
	}{
		{
			name:   "excludes alertname and severity by default",
			labels: map[string]string{"alertname": "HighCPU", "severity": "critical", "namespace": "prod", "cluster": "us-east"},
			want:   "cluster=us-east | namespace=prod",
		},
		{
			name:        "excludes extra keys",
			labels:      map[string]string{"alertname": "X", "severity": "Y", "pod": "abc", "namespace": "prod"},
			excludeKeys: []string{"pod"},
			want:        "namespace=prod",
		},
		{
			name:   "empty labels",
			labels: map[string]string{},
			want:   "",
		},
		{
			name:   "only excluded keys",
			labels: map[string]string{"alertname": "Test", "severity": "info"},
			want:   "",
		},
		{
			name:        "empty exclude key is ignored",
			labels:      map[string]string{"alertname": "X", "foo": "bar"},
			excludeKeys: []string{""},
			want:        "foo=bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatLabelsContext(tt.labels, tt.excludeKeys...)
			if got != tt.want {
				t.Errorf("formatLabelsContext() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatToolsTraceFromResult(t *testing.T) {
	tests := []struct {
		name   string
		result *domain.AnalysisResult
		want   string
	}{
		{
			name: "with ToolsTrace entries",
			result: &domain.AnalysisResult{
				ToolsTrace: []domain.ToolTraceEntry{
					{Name: "prometheus", Success: true},
					{Name: "k8s", Success: true},
					{Name: "loki", Success: false},
				},
			},
			want: "prometheus \u2713  k8s \u2713  loki \u2717",
		},
		{
			name: "fallback to ToolsUsed when no ToolsTrace",
			result: &domain.AnalysisResult{
				ToolsUsed: []string{"grafana", "kubectl"},
			},
			want: "grafana \u2713  kubectl \u2713",
		},
		{
			name:   "empty result",
			result: &domain.AnalysisResult{},
			want:   "",
		},
		{
			name: "ToolsTrace takes priority over ToolsUsed",
			result: &domain.AnalysisResult{
				ToolsTrace: []domain.ToolTraceEntry{
					{Name: "prom", Success: true},
				},
				ToolsUsed: []string{"should-not-appear"},
			},
			want: "prom \u2713",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatToolsTraceFromResult(tt.result)
			if got != tt.want {
				t.Errorf("formatToolsTraceFromResult() = %q, want %q", got, tt.want)
			}
		})
	}
}
