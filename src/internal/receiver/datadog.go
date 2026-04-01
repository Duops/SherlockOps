package receiver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
	"github.com/google/uuid"
)

// DatadogReceiver parses Datadog webhook payloads.
type DatadogReceiver struct{}

type datadogPayload struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Text      string   `json:"text"`
	AlertType string   `json:"alert_type"`
	Tags      []string `json:"tags"`
	Date      int64    `json:"date"`
}

func NewDatadogReceiver() *DatadogReceiver {
	return &DatadogReceiver{}
}

func (r *DatadogReceiver) Source() string {
	return "datadog"
}

func (r *DatadogReceiver) Parse(_ context.Context, body []byte, _ map[string]string) ([]domain.Alert, error) {
	var payload datadogPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid datadog payload: %w", err)
	}

	now := time.Now()

	severity := mapDatadogSeverity(payload.AlertType)
	labels := parseDatadogTags(payload.Tags)
	labels["alertname"] = payload.Title

	annotations := map[string]string{
		"description": payload.Text,
	}

	fingerprint := generateFingerprint(payload.Title, labels)

	var startsAt time.Time
	if payload.Date > 0 {
		startsAt = time.Unix(payload.Date, 0)
	} else {
		startsAt = now
	}

	alert := domain.Alert{
		ID:          uuid.New().String(),
		Fingerprint: fingerprint,
		Source:      r.Source(),
		Status:      domain.StatusFiring,
		Severity:    severity,
		Name:        payload.Title,
		Labels:      labels,
		Annotations: annotations,
		RawText:     domain.BuildRawText(payload.Title, string(severity), labels, annotations),
		StartsAt:    startsAt,
		ReceivedAt:  now,
	}

	return []domain.Alert{alert}, nil
}

func mapDatadogSeverity(alertType string) domain.Severity {
	switch alertType {
	case "error":
		return domain.SeverityCritical
	case "warning":
		return domain.SeverityWarning
	default:
		return domain.SeverityInfo
	}
}

func parseDatadogTags(tags []string) map[string]string {
	labels := make(map[string]string)
	for _, tag := range tags {
		parts := strings.SplitN(tag, ":", 2)
		if len(parts) == 2 {
			labels[parts[0]] = parts[1]
		} else {
			labels[tag] = "true"
		}
	}
	return labels
}
