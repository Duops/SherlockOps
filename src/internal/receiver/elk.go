package receiver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
	"github.com/google/uuid"
)

// ELKReceiver parses ElastAlert / Elasticsearch Watcher payloads.
type ELKReceiver struct{}

type elkPayload struct {
	RuleName   string                 `json:"rule_name"`
	MatchBody  map[string]interface{} `json:"match_body"`
	AlertText  string                 `json:"alert_text"`
	NumMatches int                    `json:"num_matches"`
}

func NewELKReceiver() *ELKReceiver {
	return &ELKReceiver{}
}

func (r *ELKReceiver) Source() string {
	return "elk"
}

func (r *ELKReceiver) Parse(_ context.Context, body []byte, _ map[string]string) ([]domain.Alert, error) {
	var payload elkPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid elk payload: %w", err)
	}

	now := time.Now()

	labels := map[string]string{
		"alertname":   payload.RuleName,
		"num_matches": fmt.Sprintf("%d", payload.NumMatches),
	}

	// Flatten match_body into labels.
	for k, v := range payload.MatchBody {
		labels["match_"+k] = fmt.Sprintf("%v", v)
	}

	annotations := map[string]string{
		"alert_text": payload.AlertText,
	}

	fingerprint := generateFingerprint(payload.RuleName, labels)

	alert := domain.Alert{
		ID:          uuid.New().String(),
		Fingerprint: fingerprint,
		Source:      r.Source(),
		Status:      domain.StatusFiring,
		Severity:    domain.SeverityWarning,
		Name:        payload.RuleName,
		Labels:      labels,
		Annotations: annotations,
		RawText:     domain.BuildRawText(payload.RuleName, string(domain.SeverityWarning), labels, annotations),
		StartsAt:    now,
		ReceivedAt:  now,
	}

	return []domain.Alert{alert}, nil
}
