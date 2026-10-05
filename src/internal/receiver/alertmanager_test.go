package receiver

import (
	"context"
	"strings"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
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

func TestBuildSilenceURL_EncodesSpacesAsPercent20(t *testing.T) {
	got := buildSilenceURL("https://am.example.com", map[string]string{
		"alertname": "DomainProbeFailed",
		"domain":    "99usdt.io",
	}, nil)
	want := "https://am.example.com/#/silences/new?filter=%7Balertname%3D%22DomainProbeFailed%22%2C%20domain%3D%2299usdt.io%22%7D"
	if got != want {
		t.Errorf("silence url\n got: %s\nwant: %s", got, want)
	}
	if strings.Contains(got, "+") {
		t.Error("silence url must not use '+' for spaces: Alertmanager UI decodes the fragment with decodeURIComponent")
	}
}

func TestBuildSilenceURL_KeepsOnlyAllowedLabels(t *testing.T) {
	labels := map[string]string{
		"alertname":               "CPUThrottlingHigh",
		"namespace":               "easysend",
		"container":               "repository",
		"pod":                     "repository-5dd8fcb6ff-lrbfl",
		"instance":                "cl11umod7vldt6a9l3sl-eweg",
		"beta_kubernetes_io_arch": "amd64",
		"severity":                "info",
	}
	allow := silenceLabelSet([]string{"alertname", "namespace", "container", "severity"})
	got := buildSilenceURL("https://am", labels, allow)
	want := "https://am/#/silences/new?filter=%7Balertname%3D%22CPUThrottlingHigh%22%2C%20container%3D%22repository%22%2C%20namespace%3D%22easysend%22%2C%20severity%3D%22info%22%7D"
	if got != want {
		t.Errorf("filtered silence url\n got: %s\nwant: %s", got, want)
	}

	// alertname is always kept even when the allow list omits it.
	got = buildSilenceURL("https://am", labels, silenceLabelSet([]string{"namespace"}))
	if !strings.Contains(got, "alertname%3D%22CPUThrottlingHigh%22") {
		t.Errorf("alertname must always be present: %s", got)
	}

	// nil allow list keeps every label (legacy behaviour).
	if got := buildSilenceURL("https://am", labels, nil); !strings.Contains(got, "beta_kubernetes_io_arch") {
		t.Errorf("nil allow list must keep all labels: %s", got)
	}
}

func TestAlertmanagerReceiver_SilenceLabelsDefaultAndOverride(t *testing.T) {
	body := []byte(`{"externalURL":"https://am","alerts":[{"status":"firing","labels":{"alertname":"X","namespace":"ns","pod":"p-1","instance":"i-1","job":"j"},"annotations":{}}]}`)

	r := NewAlertmanagerReceiver()
	alerts, err := r.Parse(context.Background(), body, nil)
	if err != nil || len(alerts) != 1 {
		t.Fatalf("Parse: %v, %d alerts", err, len(alerts))
	}
	u := alerts[0].Annotations["silence_url"]
	if strings.Contains(u, "pod%3D") || strings.Contains(u, "instance%3D") || !strings.Contains(u, "namespace%3D") || !strings.Contains(u, "job%3D") {
		t.Errorf("default allow list should drop pod/instance and keep namespace/job: %s", u)
	}

	r.SetSilenceLabels([]string{"alertname", "pod"})
	alerts, _ = r.Parse(context.Background(), body, nil)
	u = alerts[0].Annotations["silence_url"]
	if !strings.Contains(u, "pod%3D") || strings.Contains(u, "namespace%3D") {
		t.Errorf("override should keep only alertname and pod: %s", u)
	}
}
