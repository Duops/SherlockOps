package receiver

import (
	"context"
	"testing"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func TestZabbixReceiver_Source(t *testing.T) {
	r := NewZabbixReceiver()
	if r.Source() != "zabbix" {
		t.Errorf("expected source 'zabbix', got %q", r.Source())
	}
}

func TestZabbixReceiver_ParseFiring(t *testing.T) {
	r := NewZabbixReceiver()

	body := []byte(`{
		"event_id": "12345",
		"host": "db-server-01",
		"trigger": "MySQL replication lag",
		"severity": "High",
		"status": "PROBLEM",
		"message": "Replication lag is 300 seconds"
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}

	a := alerts[0]
	if a.Source != "zabbix" {
		t.Errorf("expected source 'zabbix', got %q", a.Source)
	}
	if a.Status != domain.StatusFiring {
		t.Errorf("expected status firing, got %q", a.Status)
	}
	if a.Severity != domain.SeverityCritical {
		t.Errorf("expected severity critical for 'High', got %q", a.Severity)
	}
	if a.Name != "MySQL replication lag" {
		t.Errorf("expected name 'MySQL replication lag', got %q", a.Name)
	}
	if a.Labels["host"] != "db-server-01" {
		t.Errorf("expected label host=db-server-01, got %q", a.Labels["host"])
	}
	if a.Labels["event_id"] != "12345" {
		t.Errorf("expected label event_id=12345, got %q", a.Labels["event_id"])
	}
	if a.Annotations["message"] != "Replication lag is 300 seconds" {
		t.Errorf("expected message annotation, got %q", a.Annotations["message"])
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
}

func TestZabbixReceiver_ParseResolved(t *testing.T) {
	r := NewZabbixReceiver()

	body := []byte(`{
		"event_id": "12345",
		"host": "db-server-01",
		"trigger": "MySQL replication lag",
		"severity": "High",
		"status": "OK",
		"message": "Replication lag back to normal"
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if alerts[0].Status != domain.StatusResolved {
		t.Errorf("expected status resolved for status=OK, got %q", alerts[0].Status)
	}
}

func TestZabbixReceiver_ParseInvalidJSON(t *testing.T) {
	r := NewZabbixReceiver()

	_, err := r.Parse(context.Background(), []byte("bad json"), nil)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestZabbixReceiver_ParseEmptyPayload(t *testing.T) {
	r := NewZabbixReceiver()

	body := []byte(`{}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a := alerts[0]
	if a.Status != domain.StatusFiring {
		t.Errorf("expected status firing for empty status, got %q", a.Status)
	}
	if a.Severity != domain.SeverityInfo {
		t.Errorf("expected default severity info, got %q", a.Severity)
	}
}

func TestMapZabbixSeverity(t *testing.T) {
	tests := []struct {
		input string
		want  domain.Severity
	}{
		{"Disaster", domain.SeverityCritical},
		{"High", domain.SeverityCritical},
		{"disaster", domain.SeverityCritical},
		{"high", domain.SeverityCritical},
		{"Average", domain.SeverityWarning},
		{"Warning", domain.SeverityWarning},
		{"average", domain.SeverityWarning},
		{"warning", domain.SeverityWarning},
		{"Information", domain.SeverityInfo},
		{"Not classified", domain.SeverityInfo},
		{"", domain.SeverityInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mapZabbixSeverity(tt.input)
			if got != tt.want {
				t.Errorf("mapZabbixSeverity(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
