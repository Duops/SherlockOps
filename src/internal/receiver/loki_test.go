package receiver

import (
	"context"
	"testing"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func TestLokiReceiver_Source(t *testing.T) {
	r := NewLokiReceiver()
	if r.Source() != "loki" {
		t.Errorf("expected source 'loki', got %q", r.Source())
	}
}

func TestLokiReceiver_ParseDelegatesToAlertmanager(t *testing.T) {
	r := NewLokiReceiver()

	body := []byte(`{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "LokiLogError", "severity": "critical"},
			"annotations": {"summary": "Too many error logs"},
			"startsAt": "2024-01-15T10:00:00Z",
			"fingerprint": "loki-fp-001"
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
	if a.Source != "loki" {
		t.Errorf("expected source 'loki', got %q", a.Source)
	}
	if a.Name != "LokiLogError" {
		t.Errorf("expected name 'LokiLogError', got %q", a.Name)
	}
	if a.Severity != domain.SeverityCritical {
		t.Errorf("expected severity critical, got %q", a.Severity)
	}
	if a.Status != domain.StatusFiring {
		t.Errorf("expected status firing, got %q", a.Status)
	}
	if a.Fingerprint != "loki-fp-001" {
		t.Errorf("expected fingerprint 'loki-fp-001', got %q", a.Fingerprint)
	}
}

func TestLokiReceiver_ParseMultipleAlerts(t *testing.T) {
	r := NewLokiReceiver()

	body := []byte(`{
		"status": "firing",
		"alerts": [
			{"status": "firing", "labels": {"alertname": "A"}, "annotations": {}, "fingerprint": "f1"},
			{"status": "resolved", "labels": {"alertname": "B"}, "annotations": {}, "fingerprint": "f2"}
		]
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(alerts))
	}

	for _, a := range alerts {
		if a.Source != "loki" {
			t.Errorf("expected all alerts to have source 'loki', got %q", a.Source)
		}
	}
}

func TestLokiReceiver_ParseInvalidJSON(t *testing.T) {
	r := NewLokiReceiver()

	_, err := r.Parse(context.Background(), []byte("not json"), nil)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
