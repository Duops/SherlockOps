package receiver

import (
	"context"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// LokiReceiver parses Loki ruler alerts. Loki uses Alertmanager format,
// so this receiver delegates to AlertmanagerReceiver and overrides the source.
type LokiReceiver struct {
	am *AlertmanagerReceiver
}

func NewLokiReceiver() *LokiReceiver {
	return &LokiReceiver{am: NewAlertmanagerReceiver()}
}

func (r *LokiReceiver) Source() string {
	return "loki"
}

func (r *LokiReceiver) Parse(ctx context.Context, body []byte, headers map[string]string) ([]domain.Alert, error) {
	alerts, err := r.am.Parse(ctx, body, headers)
	if err != nil {
		return nil, err
	}

	for i := range alerts {
		alerts[i].Source = r.Source()
	}

	return alerts, nil
}
