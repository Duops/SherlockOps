package receiver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
	"github.com/google/uuid"
)

// GrafanaReceiver parses Grafana Alerting webhook payloads.
type GrafanaReceiver struct{}

type grafanaPayload struct {
	Status string         `json:"status"`
	Alerts []grafanaAlert `json:"alerts"`
}

type grafanaAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt"`
	EndsAt      string            `json:"endsAt"`
	Fingerprint string            `json:"fingerprint"`
}

func NewGrafanaReceiver() *GrafanaReceiver {
	return &GrafanaReceiver{}
}

func (r *GrafanaReceiver) Source() string {
	return "grafana"
}

func (r *GrafanaReceiver) Parse(_ context.Context, body []byte, _ map[string]string) ([]domain.Alert, error) {
	var payload grafanaPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid grafana payload: %w", err)
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

		alerts = append(alerts, domain.Alert{
			ID:          uuid.New().String(),
			Fingerprint: fingerprint,
			Source:      r.Source(),
			Status:      status,
			Severity:    severity,
			Name:        name,
			Labels:      a.Labels,
			Annotations: a.Annotations,
			RawText:     domain.BuildRawText(name, string(severity), a.Labels, a.Annotations),
			StartsAt:    startsAt,
			EndsAt:      endsAt,
			ReceivedAt:  now,
		})
	}

	return alerts, nil
}
