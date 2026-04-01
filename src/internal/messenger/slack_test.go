package messenger

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestSlackName(t *testing.T) {
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#general", nil, testLogger())
	if s.Name() != "slack" {
		t.Errorf("expected Name() = slack, got %s", s.Name())
	}
}

func TestFormatSlackAnalysis(t *testing.T) {
	alert := &domain.Alert{
		Name:     "HighCPU",
		Severity: domain.SeverityCritical,
		Status:   domain.StatusFiring,
	}
	result := &domain.AnalysisResult{
		Text:      "CPU usage is high due to runaway process.",
		ToolsUsed: []string{"kubectl", "prometheus"},
	}

	text := formatSlackAnalysis(alert, result)

	if text == "" {
		t.Fatal("expected non-empty text")
	}
	if !contains(text, "*Alert Analysis: HighCPU*") {
		t.Error("expected alert name in bold")
	}
	if !contains(text, "_Severity: critical | Status: firing_") {
		t.Error("expected severity and status")
	}
	if !contains(text, "CPU usage is high") {
		t.Error("expected analysis text")
	}
	if !contains(text, "_Tools used: kubectl, prometheus_") {
		t.Error("expected tools used")
	}
}

func TestFormatSlackAnalysis_NoSeverity(t *testing.T) {
	alert := &domain.Alert{
		Name: "SimpleAlert",
	}
	result := &domain.AnalysisResult{
		Text: "All good.",
	}

	text := formatSlackAnalysis(alert, result)

	if contains(text, "Severity") {
		t.Error("should not contain severity when empty")
	}
	if !contains(text, "*Alert Analysis: SimpleAlert*") {
		t.Error("expected alert name")
	}
}

func TestSlackSendAnalysis(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer xoxb-test" {
			t.Error("expected bot token in Authorization header")
		}

		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	alert := &domain.Alert{
		Name:     "DiskFull",
		Severity: domain.SeverityWarning,
		Status:   domain.StatusFiring,
	}
	result := &domain.AnalysisResult{
		Text: "Disk /data is at 95%.",
	}

	ctx := context.Background()
	if err := s.SendAnalysis(ctx, alert, result); err != nil {
		t.Fatalf("SendAnalysis failed: %v", err)
	}

	if receivedBody["channel"] != "#alerts" {
		t.Errorf("expected channel #alerts, got %v", receivedBody["channel"])
	}
	if _, ok := receivedBody["thread_ts"]; ok {
		t.Error("should not have thread_ts when no reply target")
	}
}

func TestSlackSendAnalysis_InThread(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	alert := &domain.Alert{
		Name: "PodCrash",
		ReplyTarget: &domain.ReplyTarget{
			Messenger: "slack",
			Channel:   "C123456",
			ThreadID:  "1234567890.123456",
		},
	}
	result := &domain.AnalysisResult{Text: "Pod is in CrashLoopBackOff."}

	ctx := context.Background()
	if err := s.SendAnalysis(ctx, alert, result); err != nil {
		t.Fatalf("SendAnalysis failed: %v", err)
	}

	if receivedBody["channel"] != "C123456" {
		t.Errorf("expected channel C123456, got %v", receivedBody["channel"])
	}
	if receivedBody["thread_ts"] != "1234567890.123456" {
		t.Errorf("expected thread_ts, got %v", receivedBody["thread_ts"])
	}
}

func TestSlackSendError(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	alert := &domain.Alert{Name: "TestAlert"}
	ctx := context.Background()

	if err := s.SendError(ctx, alert, fmt.Errorf("analysis timeout")); err != nil {
		t.Fatalf("SendError failed: %v", err)
	}

	text, ok := receivedBody["text"].(string)
	if !ok {
		t.Fatal("expected text in body")
	}
	if !contains(text, ":warning:") {
		t.Error("expected warning emoji")
	}
	if !contains(text, "internal error occurred") {
		t.Error("expected sanitized error message")
	}
}

func TestSlackRateLimitRetry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"ok":false,"error":"rate_limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	alert := &domain.Alert{Name: "RateLimitTest"}
	result := &domain.AnalysisResult{Text: "test"}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.SendAnalysis(ctx, alert, result); err != nil {
		t.Fatalf("expected retry to succeed, got: %v", err)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func TestSlackAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "channel_not_found"})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	alert := &domain.Alert{Name: "TestAlert"}
	result := &domain.AnalysisResult{Text: "test"}

	err := s.SendAnalysis(context.Background(), alert, result)
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "channel_not_found") {
		t.Errorf("expected channel_not_found error, got: %v", err)
	}
}

func TestSlackIsListenChannel(t *testing.T) {
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", []string{"C111", "C222"}, testLogger())

	if !s.isListenChannel("C111") {
		t.Error("expected C111 to be a listen channel")
	}
	if s.isListenChannel("C999") {
		t.Error("expected C999 to not be a listen channel")
	}

	// Empty list means accept all.
	s2 := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	if !s2.isListenChannel("anything") {
		t.Error("expected any channel when listen list is empty")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
