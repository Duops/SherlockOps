package receiver

import (
	"context"
	"testing"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func TestAlertmanagerReceiver_Source(t *testing.T) {
	r := NewAlertmanagerReceiver()
	if r.Source() != "alertmanager" {
		t.Errorf("expected source 'alertmanager', got %q", r.Source())
	}
}

func TestAlertmanagerReceiver_ParseFiring(t *testing.T) {
	r := NewAlertmanagerReceiver()

	body := []byte(`{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "HighCPU", "severity": "warning", "instance": "web-01"},
			"annotations": {"summary": "CPU > 90%"},
			"startsAt": "2024-01-15T10:00:00Z",
			"endsAt": "0001-01-01T00:00:00Z",
			"fingerprint": "abc123"
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
	if a.Source != "alertmanager" {
		t.Errorf("expected source 'alertmanager', got %q", a.Source)
	}
	if a.Status != domain.StatusFiring {
		t.Errorf("expected status firing, got %q", a.Status)
	}
	if a.Severity != domain.SeverityWarning {
		t.Errorf("expected severity warning, got %q", a.Severity)
	}
	if a.Name != "HighCPU" {
		t.Errorf("expected name 'HighCPU', got %q", a.Name)
	}
	if a.Fingerprint != "abc123" {
		t.Errorf("expected fingerprint 'abc123', got %q", a.Fingerprint)
	}
	if a.ID == "" {
		t.Error("expected non-empty ID")
	}
	if a.RawText == "" {
		t.Error("expected non-empty RawText")
	}
	if a.ReceivedAt.IsZero() {
		t.Error("expected non-zero ReceivedAt")
	}
	if a.StartsAt.IsZero() {
		t.Error("expected non-zero StartsAt")
	}
}

func TestAlertmanagerReceiver_ParseResolved(t *testing.T) {
	r := NewAlertmanagerReceiver()

	body := []byte(`{
		"status": "resolved",
		"alerts": [{
			"status": "resolved",
			"labels": {"alertname": "HighCPU", "severity": "critical"},
			"annotations": {"summary": "CPU back to normal"},
			"startsAt": "2024-01-15T10:00:00Z",
			"endsAt": "2024-01-15T10:30:00Z",
			"fingerprint": "def456"
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
	if a.Status != domain.StatusResolved {
		t.Errorf("expected status resolved, got %q", a.Status)
	}
	if a.Severity != domain.SeverityCritical {
		t.Errorf("expected severity critical, got %q", a.Severity)
	}
	if a.EndsAt.IsZero() {
		t.Error("expected non-zero EndsAt")
	}
}

func TestAlertmanagerReceiver_ParseMultiple(t *testing.T) {
	r := NewAlertmanagerReceiver()

	body := []byte(`{
		"status": "firing",
		"alerts": [
			{"status": "firing", "labels": {"alertname": "A"}, "annotations": {}, "fingerprint": "f1"},
			{"status": "firing", "labels": {"alertname": "B"}, "annotations": {}, "fingerprint": "f2"}
		]
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(alerts))
	}
}

func TestAlertmanagerReceiver_ParseGeneratesFingerprint(t *testing.T) {
	r := NewAlertmanagerReceiver()

	body := []byte(`{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "NoFingerprint", "severity": "info"},
			"annotations": {}
		}]
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if alerts[0].Fingerprint == "" {
		t.Error("expected generated fingerprint when not provided")
	}
}

func TestAlertmanagerReceiver_ParseInvalidJSON(t *testing.T) {
	r := NewAlertmanagerReceiver()

	_, err := r.Parse(context.Background(), []byte("not json"), nil)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
