package receiver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
	"github.com/google/uuid"
)

// ZabbixReceiver parses Zabbix webhook media type payloads.
type ZabbixReceiver struct{}

type zabbixPayload struct {
	EventID  string `json:"event_id"`
	Host     string `json:"host"`
	Trigger  string `json:"trigger"`
	Severity string `json:"severity"`
	Status   string `json:"status"`
	Message  string `json:"message"`
}

func NewZabbixReceiver() *ZabbixReceiver {
	return &ZabbixReceiver{}
}

func (r *ZabbixReceiver) Source() string {
	return "zabbix"
}

func (r *ZabbixReceiver) Parse(_ context.Context, body []byte, _ map[string]string) ([]domain.Alert, error) {
	var payload zabbixPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid zabbix payload: %w", err)
	}

	now := time.Now()

	status := domain.StatusFiring
	if payload.Status == "OK" {
		status = domain.StatusResolved
	}

	severity := mapZabbixSeverity(payload.Severity)
	name := payload.Trigger

	labels := map[string]string{
		"host":      payload.Host,
		"alertname": name,
		"event_id":  payload.EventID,
	}
	annotations := map[string]string{
		"message": payload.Message,
	}

	fingerprint := generateFingerprint(name, labels)

	alert := domain.Alert{
		ID:          uuid.New().String(),
		Fingerprint: fingerprint,
		Source:      r.Source(),
		Status:      status,
		Severity:    severity,
		Name:        name,
		Labels:      labels,
		Annotations: annotations,
		RawText:     domain.BuildRawText(name, string(severity), labels, annotations),
		StartsAt:    now,
		ReceivedAt:  now,
	}

	return []domain.Alert{alert}, nil
}

func mapZabbixSeverity(s string) domain.Severity {
	switch s {
	case "Disaster", "High", "disaster", "high":
		return domain.SeverityCritical
	case "Average", "Warning", "average", "warning":
		return domain.SeverityWarning
	default:
		return domain.SeverityInfo
	}
}
