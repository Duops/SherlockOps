package receiver

import (
	"context"
	"testing"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func TestGenericReceiver_Source(t *testing.T) {
	r := NewGenericReceiver()
	if r.Source() != "generic" {
		t.Errorf("expected source 'generic', got %q", r.Source())
	}
}

func TestGenericReceiver_ParseWithAlertname(t *testing.T) {
	r := NewGenericReceiver()

	body := []byte(`{
		"alertname": "HighMemory",
		"severity": "warning",
		"status": "firing",
		"message": "Memory usage above 90%",
		"labels": {"host": "web-01", "env": "prod"}
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}

	a := alerts[0]
	if a.Name != "HighMemory" {
		t.Errorf("expected name 'HighMemory', got %q", a.Name)
	}
	if a.Severity != domain.SeverityWarning {
		t.Errorf("expected severity warning, got %q", a.Severity)
	}
	if a.Status != domain.StatusFiring {
		t.Errorf("expected status firing, got %q", a.Status)
	}
	if a.Source != "generic" {
		t.Errorf("expected source 'generic', got %q", a.Source)
	}
	if a.Labels["host"] != "web-01" {
		t.Errorf("expected label host=web-01, got %q", a.Labels["host"])
	}
}

func TestGenericReceiver_ParseWithTitle(t *testing.T) {
	r := NewGenericReceiver()

	body := []byte(`{
		"title": "Server Down",
		"priority": "critical",
		"description": "Server is not responding"
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a := alerts[0]
	if a.Name != "Server Down" {
		t.Errorf("expected name 'Server Down', got %q", a.Name)
	}
	if a.Severity != domain.SeverityCritical {
		t.Errorf("expected severity critical, got %q", a.Severity)
	}
	if a.Annotations["message"] != "Server is not responding" {
		t.Errorf("expected description in annotations, got %q", a.Annotations["message"])
	}
}

func TestGenericReceiver_ParseResolved(t *testing.T) {
	r := NewGenericReceiver()

	body := []byte(`{
		"name": "DiskCheck",
		"status": "resolved"
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if alerts[0].Status != domain.StatusResolved {
		t.Errorf("expected status resolved, got %q", alerts[0].Status)
	}
}

func TestGenericReceiver_ParseMinimalJSON(t *testing.T) {
	r := NewGenericReceiver()

	body := []byte(`{"foo": "bar"}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}

	a := alerts[0]
	if a.Name != "unknown" {
		t.Errorf("expected name 'unknown', got %q", a.Name)
	}
	if a.Severity != domain.SeverityInfo {
		t.Errorf("expected severity info, got %q", a.Severity)
	}
}

func TestGenericReceiver_ParseWithTagsArray(t *testing.T) {
	r := NewGenericReceiver()

	body := []byte(`{
		"title": "Alert",
		"tags": ["prod", "critical"]
	}`)

	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a := alerts[0]
	if a.Labels["prod"] != "true" {
		t.Errorf("expected tag 'prod' in labels, got %v", a.Labels)
	}
}

func TestGenericReceiver_ParseInvalidJSON(t *testing.T) {
	r := NewGenericReceiver()

	_, err := r.Parse(context.Background(), []byte("not json"), nil)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
