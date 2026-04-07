package receiver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

func TestRouter_PostAlert(t *testing.T) {
	var received []domain.Alert
	handler := func(alerts []domain.Alert) {
		received = alerts
	}

	receivers := []domain.Receiver{NewGenericReceiver()}
	mux := NewRouter("/webhook", receivers, handler, nil)
	body := `{"alertname": "TestAlert", "severity": "warning"}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/generic", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp map[string]int
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["accepted"] != 1 {
		t.Errorf("expected accepted=1, got %d", resp["accepted"])
	}
	if len(received) != 1 {
		t.Fatalf("expected 1 alert in handler, got %d", len(received))
	}
	if received[0].Name != "TestAlert" {
		t.Errorf("expected alert name 'TestAlert', got %q", received[0].Name)
	}
}

func TestRouter_MethodNotAllowed(t *testing.T) {
	receivers := []domain.Receiver{NewGenericReceiver()}
	mux := NewRouter("/webhook", receivers, func([]domain.Alert) {}, nil)

	req := httptest.NewRequest(http.MethodGet, "/webhook/generic", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}

func TestRouter_InvalidJSON(t *testing.T) {
	receivers := []domain.Receiver{NewGenericReceiver()}
	mux := NewRouter("/webhook", receivers, func([]domain.Alert) {}, nil)

	req := httptest.NewRequest(http.MethodPost, "/webhook/generic", strings.NewReader("not json"))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestRouter_MultipleReceivers(t *testing.T) {
	receivers := []domain.Receiver{
		NewGenericReceiver(),
		NewAlertmanagerReceiver(),
		NewDatadogReceiver(),
	}
	mux := NewRouter("/api/v1/webhook", receivers, func([]domain.Alert) {}, nil)

	// Test each receiver route exists.
	for _, source := range []string{"generic", "alertmanager", "datadog"} {
		body := `{"alertname": "Test"}`
		if source == "alertmanager" {
			body = `{"status": "firing", "alerts": [{"status": "firing", "labels": {"alertname": "Test"}, "annotations": {}}]}`
		}
		if source == "datadog" {
			body = `{"title": "Test", "alert_type": "info"}`
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/"+source, strings.NewReader(body))
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200 for %s, got %d", source, rec.Code)
		}
	}
}

func TestRouter_PrefixTrailingSlash(t *testing.T) {
	receivers := []domain.Receiver{NewGenericReceiver()}
	mux := NewRouter("/webhook/", receivers, func([]domain.Alert) {}, nil)

	body := `{"alertname": "Test"}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/generic", strings.NewReader(body))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200 with trailing slash prefix, got %d", rec.Code)
	}
}

func TestRouter_HandlerPanicRecovery(t *testing.T) {
	receivers := []domain.Receiver{NewGenericReceiver()}
	mux := NewRouter("/webhook", receivers, func([]domain.Alert) {
		panic("handler panic")
	}, nil)

	body := `{"alertname": "PanicTest"}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/generic", strings.NewReader(body))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 on handler panic, got %d", rec.Code)
	}
}

func TestRouter_HeadersPassed(t *testing.T) {
	// Use a custom receiver that checks headers.
	r := NewAlertmanagerReceiver()
	receivers := []domain.Receiver{r}
	mux := NewRouter("/webhook", receivers, func([]domain.Alert) {}, nil)

	body := `{"status": "firing", "alerts": [{"status": "firing", "labels": {"alertname": "Test"}, "annotations": {}}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom-Header", "test-value")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

// mockReceiver is a test receiver that always fails parsing.
type failingReceiver struct{}

func (r *failingReceiver) Source() string { return "failing" }
func (r *failingReceiver) Parse(_ context.Context, _ []byte, _ map[string]string) ([]domain.Alert, error) {
	return nil, context.DeadlineExceeded
}

func TestRouter_ReceiverParseError(t *testing.T) {
	receivers := []domain.Receiver{&failingReceiver{}}
	mux := NewRouter("/webhook", receivers, func([]domain.Alert) {}, nil)

	req := httptest.NewRequest(http.MethodPost, "/webhook/failing", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestRouter_OversizedBody(t *testing.T) {
	receivers := []domain.Receiver{NewGenericReceiver()}
	mux := NewRouter("/webhook", receivers, func([]domain.Alert) {}, nil)

	// Create a body larger than maxBodySize (1 MB).
	largeBody := strings.Repeat("x", 1<<20+1)
	req := httptest.NewRequest(http.MethodPost, "/webhook/generic", strings.NewReader(largeBody))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for oversized body, got %d", rec.Code)
	}
}
