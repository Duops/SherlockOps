package receiver

import (
	"io"
	"log/slog"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

const testChannelTemplate = `{{- if and (eq .Labels.severity "critical") (ne .Labels.cluster "dev") -}}
{{- if eq .Labels.project "pay" -}}#creon-alerts-prod-critical{{- else -}}#tech-alerts-prod-critical{{- end -}}
{{- else if .Labels.channel_full_name -}}
#{{ .Labels.channel_full_name }}
{{- else -}}
#tech-alerts-{{ .Labels.project | default "easysend" }}-{{ .Labels.cluster | default "prod" }}-{{ .Labels.channel | default "infra" }}
{{- end -}}`

func newTemplates(t *testing.T) *LabelTemplates {
	t.Helper()
	lt, err := NewLabelTemplates(`{{ .Labels.project }}-{{ .Labels.cluster }}`, testChannelTemplate)
	if err != nil {
		t.Fatalf("NewLabelTemplates: %v", err)
	}
	return lt
}

func alertWith(labels map[string]string) domain.Alert {
	return domain.Alert{Name: labels["alertname"], Labels: labels, Severity: domain.Severity(labels["severity"])}
}

func TestLabelTemplates_ChannelAndEnvironmentFromLabels(t *testing.T) {
	lt := newTemplates(t)
	cases := []struct {
		labels      map[string]string
		wantEnv     string
		wantChannel string
	}{
		{map[string]string{"project": "easysend", "cluster": "prod", "channel": "app", "severity": "warning"}, "easysend-prod", "#tech-alerts-easysend-prod-app"},
		{map[string]string{"project": "easysend", "cluster": "prod", "severity": "warning"}, "easysend-prod", "#tech-alerts-easysend-prod-infra"},
		{map[string]string{"project": "easysend-af", "cluster": "prod", "channel": "app", "severity": "info"}, "easysend-af-prod", "#tech-alerts-easysend-af-prod-app"},
		{map[string]string{"project": "platcore", "cluster": "legacy-prod", "channel": "business", "severity": "warning"}, "platcore-legacy-prod", "#tech-alerts-platcore-legacy-prod-business"},
		{map[string]string{"project": "platcore", "cluster": "legacy-prod", "channel_full_name": "tech-alerts-legacy-prod-slow-query", "severity": "warning"}, "platcore-legacy-prod", "#tech-alerts-legacy-prod-slow-query"},
		{map[string]string{"project": "easysend", "cluster": "prod", "channel": "app", "severity": "critical"}, "easysend-prod", "#tech-alerts-prod-critical"},
		{map[string]string{"project": "pay", "cluster": "prod", "severity": "critical"}, "pay-prod", "#creon-alerts-prod-critical"},
		{map[string]string{"project": "easysend", "cluster": "dev", "channel": "app", "severity": "critical"}, "easysend-dev", "#tech-alerts-easysend-dev-app"},
	}
	for _, c := range cases {
		alerts := []domain.Alert{alertWith(c.labels)}
		lt.Apply(alerts, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if alerts[0].Environment != c.wantEnv {
			t.Errorf("%v: environment = %q, want %q", c.labels, alerts[0].Environment, c.wantEnv)
		}
		if got := alerts[0].ChannelOverrides["slack"]; got != c.wantChannel {
			t.Errorf("%v: channel = %q, want %q", c.labels, got, c.wantChannel)
		}
	}
}

func TestLabelTemplates_HeadersTakePrecedenceAndPerAlertMaps(t *testing.T) {
	lt := newTemplates(t)
	shared := map[string]string{"slack": "#from-header"}
	alerts := []domain.Alert{
		{Name: "A", Environment: "from-header", ChannelOverrides: shared, Labels: map[string]string{"project": "easysend", "cluster": "prod", "channel": "app"}},
		{Name: "B", Labels: map[string]string{"project": "easysend", "cluster": "prod", "channel": "app"}},
		{Name: "C", Labels: map[string]string{"project": "easysend", "cluster": "prod", "channel": "business"}},
	}
	lt.Apply(alerts, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if alerts[0].Environment != "from-header" || alerts[0].ChannelOverrides["slack"] != "#from-header" {
		t.Errorf("header values must win: %+v", alerts[0])
	}
	if alerts[1].ChannelOverrides["slack"] != "#tech-alerts-easysend-prod-app" || alerts[2].ChannelOverrides["slack"] != "#tech-alerts-easysend-prod-business" {
		t.Errorf("per-alert channels: %q / %q", alerts[1].ChannelOverrides["slack"], alerts[2].ChannelOverrides["slack"])
	}
	if shared["slack"] != "#from-header" {
		t.Error("shared header map must not be mutated")
	}
}

func TestLabelTemplates_InvalidEnvironmentAndEmptyChannelIgnored(t *testing.T) {
	lt, err := NewLabelTemplates(`{{ .Labels.env }}`, `{{ .Labels.chan }}`)
	if err != nil {
		t.Fatal(err)
	}
	alerts := []domain.Alert{{Name: "X", Labels: map[string]string{"env": "bad env/with spaces", "chan": "   "}}}
	lt.Apply(alerts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if alerts[0].Environment != "" {
		t.Errorf("invalid environment must be dropped, got %q", alerts[0].Environment)
	}
	if alerts[0].ChannelOverrides != nil {
		t.Errorf("empty channel must not set overrides, got %v", alerts[0].ChannelOverrides)
	}
}

func TestNewLabelTemplates_Errors(t *testing.T) {
	if _, err := NewLabelTemplates(`{{ .Labels.project `, ""); err == nil {
		t.Error("expected parse error for environment template")
	}
	if _, err := NewLabelTemplates("", `{{ nosuchfunc }}`); err == nil {
		t.Error("expected parse error for channel template")
	}
	lt, err := NewLabelTemplates("", "")
	if err != nil || lt != nil {
		t.Errorf("empty templates should yield nil, got %v, %v", lt, err)
	}
}
