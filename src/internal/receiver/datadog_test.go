package receiver

import (
	"context"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

func TestDatadogReceiver_Source(t *testing.T) {
	r := NewDatadogReceiver()
	if r.Source() != "datadog" {
		t.Errorf("expected source 'datadog', got %q", r.Source())
	}
}

func TestDatadogReceiver_ParseFiring(t *testing.T) {
	r := NewDatadogReceiver()

	body := []byte(`{
		"id": "evt-123",
		"title": "High CPU on web-01",
		"text": "CPU is above 90% for 5 minutes",
		"alert_type": "error",
		"tags": ["env:prod", "host:web-01", "critical"],
		"date": 1705312800
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}

	a := alerts[0]
	if a.Source != "datadog" {
		t.Errorf("expected source 'datadog', got %q", a.Source)
	}
	if a.Status != domain.StatusFiring {
		t.Errorf("expected status firing, got %q", a.Status)
	}
	if a.Severity != domain.SeverityCritical {
		t.Errorf("expected severity critical for alert_type=error, got %q", a.Severity)
	}
	if a.Name != "High CPU on web-01" {
		t.Errorf("expected name 'High CPU on web-01', got %q", a.Name)
	}
	if a.Annotations["description"] != "CPU is above 90% for 5 minutes" {
		t.Errorf("expected description annotation, got %q", a.Annotations["description"])
	}
	if a.Labels["env"] != "prod" {
		t.Errorf("expected label env=prod, got %q", a.Labels["env"])
	}
	if a.Labels["host"] != "web-01" {
		t.Errorf("expected label host=web-01, got %q", a.Labels["host"])
	}
	if a.Labels["critical"] != "true" {
		t.Errorf("expected tag 'critical' as label, got %q", a.Labels["critical"])
	}
	if a.ID == "" {
		t.Error("expected non-empty ID")
	}
	if a.Fingerprint == "" {
		t.Error("expected non-empty Fingerprint")
	}
	if a.RawText == "" {
		t.Error("expected non-empty RawText")
	}
	if a.StartsAt.Unix() != 1705312800 {
		t.Errorf("expected StartsAt from date field, got %v", a.StartsAt)
	}
}

func TestDatadogReceiver_ParseNoDate(t *testing.T) {
	r := NewDatadogReceiver()

	body := []byte(`{
		"title": "Disk Warning",
		"text": "Disk usage high",
		"alert_type": "warning",
		"tags": []
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a := alerts[0]
	if a.Severity != domain.SeverityWarning {
		t.Errorf("expected severity warning, got %q", a.Severity)
	}
	if a.StartsAt.IsZero() {
		t.Error("expected non-zero StartsAt when date is missing")
	}
}

func TestDatadogReceiver_ParseInvalidJSON(t *testing.T) {
	r := NewDatadogReceiver()

	_, err := r.Parse(context.Background(), []byte("not json"), nil)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestDatadogReceiver_ParseEmptyPayload(t *testing.T) {
	r := NewDatadogReceiver()

	body := []byte(`{}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}

	a := alerts[0]
	if a.Name != "" {
		t.Errorf("expected empty name for empty payload, got %q", a.Name)
	}
	if a.Severity != domain.SeverityInfo {
		t.Errorf("expected default severity info, got %q", a.Severity)
	}
}

func TestMapDatadogSeverity(t *testing.T) {
	tests := []struct {
		input string
		want  domain.Severity
	}{
		{"error", domain.SeverityCritical},
		{"warning", domain.SeverityWarning},
		{"info", domain.SeverityInfo},
		{"success", domain.SeverityInfo},
		{"", domain.SeverityInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mapDatadogSeverity(tt.input)
			if got != tt.want {
				t.Errorf("mapDatadogSeverity(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDatadogTags(t *testing.T) {
	tags := []string{"env:prod", "host:web-01", "standalone", "region:us-east-1"}
	labels := parseDatadogTags(tags)

	if labels["env"] != "prod" {
		t.Errorf("expected env=prod, got %q", labels["env"])
	}
	if labels["host"] != "web-01" {
		t.Errorf("expected host=web-01, got %q", labels["host"])
	}
	if labels["standalone"] != "true" {
		t.Errorf("expected standalone=true, got %q", labels["standalone"])
	}
	if labels["region"] != "us-east-1" {
		t.Errorf("expected region=us-east-1, got %q", labels["region"])
	}
}

func TestParseDatadogTags_Empty(t *testing.T) {
	labels := parseDatadogTags(nil)
	if len(labels) != 0 {
		t.Errorf("expected empty labels for nil tags, got %v", labels)
	}
}

func TestParseDatadogTags_ColonInValue(t *testing.T) {
	tags := []string{"url:http://example.com:8080"}
	labels := parseDatadogTags(tags)

	if labels["url"] != "http://example.com:8080" {
		t.Errorf("expected url with colon preserved in value, got %q", labels["url"])
	}
}
