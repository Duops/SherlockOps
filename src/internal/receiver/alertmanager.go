package receiver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
	"github.com/google/uuid"
)

// AlertmanagerReceiver parses Alertmanager webhook v4 payloads.
type AlertmanagerReceiver struct{}

type alertmanagerPayload struct {
	Status      string              `json:"status"`
	GroupLabels map[string]string   `json:"groupLabels"`
	ExternalURL string              `json:"externalURL"`
	Alerts      []alertmanagerAlert `json:"alerts"`
}

type alertmanagerAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	Fingerprint  string            `json:"fingerprint"`
	GeneratorURL string            `json:"generatorURL"`
}

func NewAlertmanagerReceiver() *AlertmanagerReceiver {
	return &AlertmanagerReceiver{}
}

func (r *AlertmanagerReceiver) Source() string {
	return "alertmanager"
}

func (r *AlertmanagerReceiver) Parse(_ context.Context, body []byte, _ map[string]string) ([]domain.Alert, error) {
	var payload alertmanagerPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid alertmanager payload: %w", err)
	}

	now := time.Now()
	alerts := make([]domain.Alert, 0, len(payload.Alerts))

	for _, a := range payload.Alerts {
		status := domain.StatusFiring
		if a.Status == "resolved" {
			status = domain.StatusResolved
		}

		severity := mapSeverity(a.Labels["severity"])
		name := a.Labels["alertname"]

		fingerprint := a.Fingerprint
		if fingerprint == "" {
			fingerprint = generateFingerprint(name, a.Labels)
		}

		startsAt, _ := time.Parse(time.RFC3339, a.StartsAt)
		endsAt, _ := time.Parse(time.RFC3339, a.EndsAt)

		// Enrich annotations with action URLs.
		annots := make(map[string]string)
		for k, v := range a.Annotations {
			annots[k] = v
		}
		if a.GeneratorURL != "" {
			annots["generator_url"] = a.GeneratorURL
		}
		if payload.ExternalURL != "" {
			annots["silence_url"] = buildSilenceURL(payload.ExternalURL, a.Labels)
		}

		alerts = append(alerts, domain.Alert{
			ID:          uuid.New().String(),
			Fingerprint: fingerprint,
			Source:      r.Source(),
			Status:      status,
			Severity:    severity,
			Name:        name,
			Labels:      a.Labels,
			Annotations: annots,
			RawText:     domain.BuildRawText(name, string(severity), a.Labels, a.Annotations),
			StartsAt:    startsAt,
			EndsAt:      endsAt,
			ReceivedAt:  now,
		})
	}

	return alerts, nil
}

// buildSilenceURL creates an Alertmanager silence creation URL from labels.
func buildSilenceURL(externalURL string, labels map[string]string) string {
	var matchers []string
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		matchers = append(matchers, fmt.Sprintf(`%s="%s"`, k, labels[k]))
	}
	filter := "{" + strings.Join(matchers, ", ") + "}"
	return externalURL + "/#/silences/new?filter=" + url.QueryEscape(filter)
}

// GroupAlerts groups alerts by alertname into combined alerts.
// Single alerts pass through unchanged. Multiple alerts with the same name
// are merged into one with GroupedAlerts populated.
func GroupAlerts(alerts []domain.Alert) []domain.Alert {
	groups := make(map[string][]int)
	var order []string
	for i, a := range alerts {
		if _, seen := groups[a.Name]; !seen {
			order = append(order, a.Name)
		}
		groups[a.Name] = append(groups[a.Name], i)
	}

	result := make([]domain.Alert, 0, len(order))
	for _, name := range order {
		indices := groups[name]
		if len(indices) == 1 {
			result = append(result, alerts[indices[0]])
			continue
		}

		// Multiple alerts — group them.
		base := alerts[indices[0]]
		base.GroupedAlerts = make([]*domain.Alert, len(indices))
		for j, idx := range indices {
			a := alerts[idx]
			base.GroupedAlerts[j] = &a
		}
		// Use highest severity in the group.
		for _, idx := range indices {
			if alerts[idx].Severity == domain.SeverityCritical {
				base.Severity = domain.SeverityCritical
				break
			}
		}
		result = append(result, base)
	}
	return result
}

func mapSeverity(s string) domain.Severity {
	switch s {
	case "critical":
		return domain.SeverityCritical
	case "warning":
		return domain.SeverityWarning
	default:
		return domain.SeverityInfo
	}
}
