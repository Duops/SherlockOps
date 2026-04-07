package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerExposesRegisteredMetrics(t *testing.T) {
	// Touch a few counters to make sure they appear in the output.
	AlertsReceived.WithLabelValues("alertmanager").Inc()
	AlertsAnalyzed.WithLabelValues("success").Inc()
	CacheHits.Inc()
	CacheMisses.Inc()
	TokensTotal.WithLabelValues("input").Add(123)
	CostTotal.Add(0.42)
	MessengerDeliveryTotal.WithLabelValues("slack", "success").Inc()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from metrics handler, got %d", rec.Code)
	}

	body := rec.Body.String()
	// Spot-check the metric families that this codebase actually relies on.
	required := []string{
		"alerts_received_total",
		"alerts_analyzed_total",
		"cache_hits_total",
		"cache_misses_total",
		"llm_tokens_total",
		"llm_cost_dollars_total",
		"messenger_delivery_total",
	}
	for _, name := range required {
		if !strings.Contains(body, name) {
			t.Errorf("expected metric %q in /metrics output", name)
		}
	}
}
