package receiver

import (
	"context"
	"testing"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func TestGrafanaReceiver_Source(t *testing.T) {
	r := NewGrafanaReceiver()
	if r.Source() != "grafana" {
		t.Errorf("expected source 'grafana', got %q", r.Source())
	}
}

func TestGrafanaReceiver_ParseFiring(t *testing.T) {
	r := NewGrafanaReceiver()

	body := []byte(`{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "DiskFull", "severity": "critical"},
			"annotations": {"summary": "Disk usage > 90%"},
			"startsAt": "2024-01-15T10:00:00Z",
			"endsAt": "0001-01-01T00:00:00Z",
			"fingerprint": "graf123"
		}]
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}

	a := alerts[0]
	if a.Source != "grafana" {
		t.Errorf("expected source 'grafana', got %q", a.Source)
	}
	if a.Status != domain.StatusFiring {
		t.Errorf("expected status firing, got %q", a.Status)
	}
	if a.Severity != domain.SeverityCritical {
		t.Errorf("expected severity critical, got %q", a.Severity)
	}
	if a.Name != "DiskFull" {
		t.Errorf("expected name 'DiskFull', got %q", a.Name)
	}
	if a.Fingerprint != "graf123" {
		t.Errorf("expected fingerprint 'graf123', got %q", a.Fingerprint)
	}
}

func TestGrafanaReceiver_ParseResolved(t *testing.T) {
	r := NewGrafanaReceiver()

	body := []byte(`{
		"status": "resolved",
		"alerts": [{
			"status": "resolved",
			"labels": {"alertname": "DiskFull", "severity": "warning"},
			"annotations": {"summary": "Disk back to normal"},
			"startsAt": "2024-01-15T10:00:00Z",
			"endsAt": "2024-01-15T11:00:00Z",
			"fingerprint": "graf456"
		}]
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if alerts[0].Status != domain.StatusResolved {
		t.Errorf("expected status resolved, got %q", alerts[0].Status)
	}
}

func TestGrafanaReceiver_ParseInvalidJSON(t *testing.T) {
	r := NewGrafanaReceiver()

	_, err := r.Parse(context.Background(), []byte("{bad}"), nil)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
