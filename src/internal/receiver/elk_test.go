package receiver

import (
	"context"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

func TestELKReceiver_Source(t *testing.T) {
	r := NewELKReceiver()
	if r.Source() != "elk" {
		t.Errorf("expected source 'elk', got %q", r.Source())
	}
}

func TestELKReceiver_ParseValid(t *testing.T) {
	r := NewELKReceiver()

	body := []byte(`{
		"rule_name": "HighErrorRate",
		"match_body": {"host": "web-01", "count": 42},
		"alert_text": "Error rate exceeded threshold",
		"num_matches": 5
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}

	a := alerts[0]
	if a.Source != "elk" {
		t.Errorf("expected source 'elk', got %q", a.Source)
	}
	if a.Status != domain.StatusFiring {
		t.Errorf("expected status firing, got %q", a.Status)
	}
	if a.Severity != domain.SeverityWarning {
		t.Errorf("expected severity warning, got %q", a.Severity)
	}
	if a.Name != "HighErrorRate" {
		t.Errorf("expected name 'HighErrorRate', got %q", a.Name)
	}
	if a.Labels["alertname"] != "HighErrorRate" {
		t.Errorf("expected alertname label, got %q", a.Labels["alertname"])
	}
	if a.Labels["num_matches"] != "5" {
		t.Errorf("expected num_matches=5, got %q", a.Labels["num_matches"])
	}
	if a.Labels["match_host"] != "web-01" {
		t.Errorf("expected match_host=web-01, got %q", a.Labels["match_host"])
	}
	if a.Labels["match_count"] != "42" {
		t.Errorf("expected match_count=42, got %q", a.Labels["match_count"])
	}
	if a.Annotations["alert_text"] != "Error rate exceeded threshold" {
		t.Errorf("expected alert_text annotation, got %q", a.Annotations["alert_text"])
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
	if a.ReceivedAt.IsZero() {
		t.Error("expected non-zero ReceivedAt")
	}
}

func TestELKReceiver_ParseInvalidJSON(t *testing.T) {
	r := NewELKReceiver()

	_, err := r.Parse(context.Background(), []byte("invalid"), nil)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestELKReceiver_ParseEmptyPayload(t *testing.T) {
	r := NewELKReceiver()

	body := []byte(`{}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}

	a := alerts[0]
	if a.Labels["num_matches"] != "0" {
		t.Errorf("expected num_matches=0 for empty payload, got %q", a.Labels["num_matches"])
	}
}

func TestELKReceiver_ParseEmptyMatchBody(t *testing.T) {
	r := NewELKReceiver()

	body := []byte(`{
		"rule_name": "TestRule",
		"match_body": {},
		"alert_text": "test",
		"num_matches": 1
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a := alerts[0]
	if a.Name != "TestRule" {
		t.Errorf("expected name 'TestRule', got %q", a.Name)
	}
}
