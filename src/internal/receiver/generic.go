package receiver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
	"github.com/google/uuid"
)

// GenericReceiver accepts any JSON payload and does best-effort field extraction.
type GenericReceiver struct{}

func NewGenericReceiver() *GenericReceiver {
	return &GenericReceiver{}
}

func (r *GenericReceiver) Source() string {
	return "generic"
}

func (r *GenericReceiver) Parse(_ context.Context, body []byte, _ map[string]string) ([]domain.Alert, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON payload: %w", err)
	}

	now := time.Now()

	name := extractString(raw, "alertname", "name", "title", "rule_name", "alert")
	if name == "" {
		name = "unknown"
	}

	severityStr := extractString(raw, "severity", "priority", "level")
	severity := mapSeverity(severityStr)

	statusStr := extractString(raw, "status", "state")
	status := domain.StatusFiring
	if statusStr == "resolved" || statusStr == "ok" || statusStr == "OK" {
		status = domain.StatusResolved
	}

	message := extractString(raw, "message", "text", "description", "summary", "alert_text")

	labels := extractLabelsMap(raw)
	if _, ok := labels["alertname"]; !ok {
		labels["alertname"] = name
	}

	annotations := make(map[string]string)
	if message != "" {
		annotations["message"] = message
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

// extractString returns the first non-empty string value found under the given keys.
func extractString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// extractLabelsMap attempts to find a labels or tags field and convert it to map[string]string.
func extractLabelsMap(m map[string]interface{}) map[string]string {
	labels := make(map[string]string)

	for _, key := range []string{"labels", "tags"} {
		if v, ok := m[key]; ok {
			switch val := v.(type) {
			case map[string]interface{}:
				for k, vv := range val {
					labels[k] = fmt.Sprintf("%v", vv)
				}
				return labels
			case []interface{}:
				for _, item := range val {
					if s, ok := item.(string); ok {
						labels[s] = "true"
					}
				}
				return labels
			}
		}
	}

	return labels
}
