package messenger

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func TestSlackStartStop(t *testing.T) {
	// Start opens a socket mode connection, so we mock it.
	// For Start, we only verify that handler is set and cancel is stored.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth.test":
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "user_id": "U123"})
		case "/apps.connections.open":
			// Return an invalid URL so the websocket fails quickly.
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": false})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	var handlerCalled bool
	err := s.Start(context.Background(), func(alert *domain.Alert) {
		handlerCalled = true
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	// Verify handler is set.
	if s.handler == nil {
		t.Error("expected handler to be set after Start")
	}

	// Stop should cancel the context.
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

	// Double Stop should be safe.
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("double Stop returned error: %v", err)
	}

	_ = handlerCalled
}

func TestSlackResolveBotUserID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth.test" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer xoxb-test" {
			t.Error("expected bot token in Authorization header")
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "user_id": "U12345"})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	err := s.resolveBotUserID(context.Background())
	if err != nil {
		t.Fatalf("resolveBotUserID failed: %v", err)
	}
	if s.botUserID != "U12345" {
		t.Errorf("expected botUserID 'U12345', got %q", s.botUserID)
	}
}

func TestSlackResolveBotUserID_NotOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	err := s.resolveBotUserID(context.Background())
	if err != nil {
		t.Fatalf("resolveBotUserID should not return error for ok=false: %v", err)
	}
	if s.botUserID != "" {
		t.Errorf("expected empty botUserID when ok=false, got %q", s.botUserID)
	}
}

func TestSlackFetchParentMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.replies" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"messages": []map[string]string{
				{"text": "Original alert message"},
			},
		})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	text := s.fetchParentMessage(context.Background(), "C123", "1234.5678")
	if text != "Original alert message" {
		t.Errorf("expected 'Original alert message', got %q", text)
	}
}

func TestSlackFetchParentMessage_NotOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	text := s.fetchParentMessage(context.Background(), "C123", "1234.5678")
	if text != "" {
		t.Errorf("expected empty string when not ok, got %q", text)
	}
}

func TestSlackFetchParentMessage_EmptyMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "messages": []interface{}{}})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	text := s.fetchParentMessage(context.Background(), "C123", "1234.5678")
	if text != "" {
		t.Errorf("expected empty string for empty messages, got %q", text)
	}
}

func TestSlackHandleEventPayload_MessageEvent(t *testing.T) {
	var mu sync.Mutex
	var receivedAlert *domain.Alert

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.botUserID = "U999"
	s.handler = func(alert *domain.Alert) {
		mu.Lock()
		receivedAlert = alert
		mu.Unlock()
	}

	payload := slackEventPayload{
		Event: slackEvent{
			Type:    "message",
			Text:    "Server is down!",
			User:    "U123",
			Channel: "C111",
			TS:      "1234567890.000001",
		},
	}

	raw, _ := json.Marshal(payload)
	s.handleEventPayload(context.Background(), raw)

	mu.Lock()
	defer mu.Unlock()
	if receivedAlert == nil {
		t.Fatal("expected handler to be called")
	}
	if receivedAlert.Source != "slack" {
		t.Errorf("expected source 'slack', got %q", receivedAlert.Source)
	}
	if receivedAlert.RawText != "Server is down!" {
		t.Errorf("expected RawText 'Server is down!', got %q", receivedAlert.RawText)
	}
	if receivedAlert.ReplyTarget.ThreadID != "1234567890.000001" {
		t.Errorf("expected ThreadID from TS, got %q", receivedAlert.ReplyTarget.ThreadID)
	}
}

func TestSlackHandleEventPayload_SkipsBotMessages(t *testing.T) {
	called := false
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.handler = func(alert *domain.Alert) {
		called = true
	}

	payload := slackEventPayload{
		Event: slackEvent{
			Type:    "message",
			Text:    "Bot message",
			BotID:   "B123",
			Channel: "C111",
		},
	}

	raw, _ := json.Marshal(payload)
	s.handleEventPayload(context.Background(), raw)

	if called {
		t.Error("expected handler NOT to be called for bot messages")
	}
}

func TestSlackHandleEventPayload_SkipsOwnMessages(t *testing.T) {
	called := false
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.botUserID = "U999"
	s.handler = func(alert *domain.Alert) {
		called = true
	}

	payload := slackEventPayload{
		Event: slackEvent{
			Type:    "message",
			Text:    "My own message",
			User:    "U999",
			Channel: "C111",
		},
	}

	raw, _ := json.Marshal(payload)
	s.handleEventPayload(context.Background(), raw)

	if called {
		t.Error("expected handler NOT to be called for own messages")
	}
}

func TestSlackHandleEventPayload_SkipsSubtypes(t *testing.T) {
	called := false
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.handler = func(alert *domain.Alert) {
		called = true
	}

	for _, subtype := range []string{"message_changed", "message_deleted"} {
		payload := slackEventPayload{
			Event: slackEvent{
				Type:    "message",
				SubType: subtype,
				Text:    "Changed",
				Channel: "C111",
			},
		}

		raw, _ := json.Marshal(payload)
		s.handleEventPayload(context.Background(), raw)

		if called {
			t.Errorf("expected handler NOT to be called for subtype %q", subtype)
		}
	}
}

func TestSlackHandleEventPayload_NonMessageType(t *testing.T) {
	called := false
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.handler = func(alert *domain.Alert) {
		called = true
	}

	payload := slackEventPayload{
		Event: slackEvent{
			Type: "reaction_added",
		},
	}

	raw, _ := json.Marshal(payload)
	s.handleEventPayload(context.Background(), raw)

	if called {
		t.Error("expected handler NOT to be called for non-message events")
	}
}

func TestSlackHandleEventPayload_ChannelFilter(t *testing.T) {
	called := false
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", []string{"C111"}, testLogger())
	s.handler = func(alert *domain.Alert) {
		called = true
	}

	payload := slackEventPayload{
		Event: slackEvent{
			Type:    "message",
			Text:    "test",
			User:    "U123",
			Channel: "C999", // Not in listen list
		},
	}

	raw, _ := json.Marshal(payload)
	s.handleEventPayload(context.Background(), raw)

	if called {
		t.Error("expected handler NOT to be called for channel not in listen list")
	}
}

func TestSlackHandleEventPayload_NilHandler(t *testing.T) {
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	// handler is nil

	payload := slackEventPayload{
		Event: slackEvent{
			Type:    "message",
			Text:    "test",
			User:    "U123",
			Channel: "C111",
		},
	}

	raw, _ := json.Marshal(payload)
	// Should not panic.
	s.handleEventPayload(context.Background(), raw)
}

func TestSlackHandleEventPayload_BotMentionInThread(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"messages": []map[string]string{
				{"text": "Parent alert text"},
			},
		})
	}))
	defer server.Close()

	var receivedAlert *domain.Alert
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL
	s.botUserID = "U999"
	s.handler = func(alert *domain.Alert) {
		receivedAlert = alert
	}

	payload := slackEventPayload{
		Event: slackEvent{
			Type:     "message",
			Text:     "<@U999> reanalyze this",
			User:     "U123",
			Channel:  "C111",
			ThreadTS: "1234.5678",
		},
	}

	raw, _ := json.Marshal(payload)
	s.handleEventPayload(context.Background(), raw)

	if receivedAlert == nil {
		t.Fatal("expected handler to be called for bot mention")
	}
	if receivedAlert.UserCommand != "reanalyze this" {
		t.Errorf("expected UserCommand 'reanalyze this', got %q", receivedAlert.UserCommand)
	}
	if receivedAlert.RawText != "Parent alert text" {
		t.Errorf("expected RawText from parent message, got %q", receivedAlert.RawText)
	}
	if receivedAlert.ReplyTarget.ThreadID != "1234.5678" {
		t.Errorf("expected ThreadID '1234.5678', got %q", receivedAlert.ReplyTarget.ThreadID)
	}
}

func TestSlackHandleEventPayload_InvalidPayload(t *testing.T) {
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.handler = func(alert *domain.Alert) {}

	// Should not panic on invalid JSON.
	s.handleEventPayload(context.Background(), []byte("not json"))
}

func TestSlackHandleAlertMessage_ThreadTS(t *testing.T) {
	var receivedAlert *domain.Alert
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.handler = func(alert *domain.Alert) {
		receivedAlert = alert
	}

	evt := slackEvent{
		Type:     "message",
		Text:     "Alert in thread",
		User:     "U123",
		Channel:  "C111",
		TS:       "1234.0001",
		ThreadTS: "1234.0000",
	}
	s.handleAlertMessage(evt)

	if receivedAlert == nil {
		t.Fatal("expected handler to be called")
	}
	if receivedAlert.ReplyTarget.ThreadID != "1234.0000" {
		t.Errorf("expected ThreadID from ThreadTS, got %q", receivedAlert.ReplyTarget.ThreadID)
	}
}

func TestSlackResolveTarget_NoReplyTarget(t *testing.T) {
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#default", nil, testLogger())

	alert := &domain.Alert{Name: "Test"}
	channel, threadTS := s.resolveTarget(alert)

	if channel != "#default" {
		t.Errorf("expected default channel '#default', got %q", channel)
	}
	if threadTS != "" {
		t.Errorf("expected empty threadTS, got %q", threadTS)
	}
}

func TestSlackResolveTarget_DifferentMessenger(t *testing.T) {
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#default", nil, testLogger())

	alert := &domain.Alert{
		Name: "Test",
		ReplyTarget: &domain.ReplyTarget{
			Messenger: "telegram",
			Channel:   "12345",
		},
	}
	channel, _ := s.resolveTarget(alert)

	if channel != "#default" {
		t.Errorf("expected default channel for non-slack messenger, got %q", channel)
	}
}

func TestSlackSendError_NoLeak(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	// Error with sensitive info that should NOT appear in the message.
	alert := &domain.Alert{Name: "DBAlert"}

	s.SendError(context.Background(), alert, fmt.Errorf("connection to %s failed: timeout", "postgres://user:password@host:5432/db"))

	text := receivedBody["text"].(string)
	if contains(text, "password") {
		t.Error("error message should not contain sensitive details")
	}
	if contains(text, "postgres://") {
		t.Error("error message should not contain connection strings")
	}
	if !contains(text, "internal error occurred") {
		t.Error("expected sanitized error message")
	}
}

func TestSlackOpenConnection_Failure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	_, err := s.openConnection(context.Background())
	if err == nil {
		t.Error("expected error when apps.connections.open fails")
	}
}

func TestSlackDoWithRetry_MaxRetriesExceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"ok":false}`))
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := s.doWithRetry(ctx, server.URL+"/test", []byte(`{}`))
	if err == nil {
		t.Error("expected error after context timeout during retries")
	}
}

func TestFormatSlackAnalysisRich(t *testing.T) {
	result := &domain.AnalysisResult{
		Text: "High CPU caused by runaway process.",
		ToolsTrace: []domain.ToolTraceEntry{
			{Name: "prometheus", Success: true},
			{Name: "loki", Success: false},
		},
	}

	text := formatSlackAnalysisRich(result)

	if !contains(text, "SherlockOps Investigation") {
		t.Error("expected investigation header")
	}
	if !contains(text, "High CPU caused by runaway process.") {
		t.Error("expected analysis text")
	}
	if !contains(text, "Tools:") {
		t.Error("expected tools footer")
	}
	if !contains(text, "prometheus") {
		t.Error("expected prometheus in tools trace")
	}
}

func TestFormatSlackAnalysisRich_NoTools(t *testing.T) {
	result := &domain.AnalysisResult{
		Text: "Simple analysis.",
	}

	text := formatSlackAnalysisRich(result)

	if !contains(text, "Simple analysis.") {
		t.Error("expected analysis text")
	}
	if contains(text, "Tools:") {
		t.Error("should not contain tools footer when no tools used")
	}
}

func TestSlackSendAlert(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "ts": "1234567890.000001"})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	alert := &domain.Alert{
		Name:     "HighCPU",
		Severity: domain.SeverityCritical,
		Status:   domain.StatusFiring,
		Labels: map[string]string{
			"pod":       "nginx-abc",
			"cluster":   "us-east",
			"namespace": "prod",
		},
		Annotations: map[string]string{
			"summary": "CPU usage is above 90%",
		},
	}

	ref, err := s.SendAlert(context.Background(), alert)
	if err != nil {
		t.Fatalf("SendAlert failed: %v", err)
	}
	if ref == nil {
		t.Fatal("expected non-nil MessageRef")
	}
	if ref.Messenger != "slack" {
		t.Errorf("expected messenger 'slack', got %q", ref.Messenger)
	}
	if ref.MessageID != "1234567890.000001" {
		t.Errorf("expected MessageID '1234567890.000001', got %q", ref.MessageID)
	}
	if ref.Channel != "#alerts" {
		t.Errorf("expected channel '#alerts', got %q", ref.Channel)
	}
}

func TestSlackSendAlert_Resolved(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "ts": "1234.0001"})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	alert := &domain.Alert{
		Name:     "HighCPU",
		Severity: domain.SeverityCritical,
		Status:   domain.StatusResolved,
		Labels:   map[string]string{},
	}

	ref, err := s.SendAlert(context.Background(), alert)
	if err != nil {
		t.Fatalf("SendAlert failed: %v", err)
	}
	if ref == nil {
		t.Fatal("expected non-nil MessageRef")
	}
}

func TestSlackSendAlert_GroupedAlerts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "ts": "1234.0001"})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	alert := &domain.Alert{
		Name:     "HighCPU",
		Severity: domain.SeverityCritical,
		Status:   domain.StatusFiring,
		Labels:   map[string]string{"pod": "main-pod"},
		GroupedAlerts: []*domain.Alert{
			{Labels: map[string]string{"pod": "pod-1", "instance": "10.0.0.1"}},
			{Labels: map[string]string{"pod": "pod-2"}},
		},
	}

	ref, err := s.SendAlert(context.Background(), alert)
	if err != nil {
		t.Fatalf("SendAlert failed: %v", err)
	}
	if ref == nil {
		t.Fatal("expected non-nil MessageRef")
	}
}

func TestSlackSendAlert_WithButtons(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "ts": "1234.0001"})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	alert := &domain.Alert{
		Name:     "DiskFull",
		Severity: domain.SeverityWarning,
		Status:   domain.StatusFiring,
		Labels:   map[string]string{},
		Annotations: map[string]string{
			"generator_url": "http://grafana/query",
			"runbook_url":   "http://wiki/runbook",
			"silence_url":   "http://alertmanager/silence",
		},
	}

	_, err := s.SendAlert(context.Background(), alert)
	if err != nil {
		t.Fatalf("SendAlert failed: %v", err)
	}
}

func TestSlackSendAnalysisReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "ts": "1234.0002"})
	}))
	defer server.Close()

	s := NewSlack("xoxb-test", "xapp-test", "secret", "#alerts", nil, testLogger())
	s.baseURL = server.URL

	ref := &domain.MessageRef{
		Messenger: "slack",
		Channel:   "#alerts",
		MessageID: "1234.0001",
		Alert:     &domain.Alert{Name: "TestAlert"},
	}
	result := &domain.AnalysisResult{
		Text: "Analysis text here.",
		ToolsTrace: []domain.ToolTraceEntry{
			{Name: "prometheus", Success: true},
		},
	}

	err := s.SendAnalysisReply(context.Background(), ref, result)
	if err != nil {
		t.Fatalf("SendAnalysisReply failed: %v", err)
	}
}

func TestSlackResolveTarget_ChannelOverride(t *testing.T) {
	s := NewSlack("xoxb-test", "xapp-test", "secret", "#default", nil, testLogger())

	alert := &domain.Alert{
		Name:             "Test",
		ChannelOverrides: map[string]string{"slack": "#overridden"},
	}
	channel, _ := s.resolveTarget(alert)

	if channel != "#overridden" {
		t.Errorf("expected channel '#overridden', got %q", channel)
	}
}

func TestSlackStartWithoutAppToken(t *testing.T) {
	s := NewSlack("xoxb-test", "", "secret", "#alerts", nil, testLogger())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "user_id": "U123"})
	}))
	defer server.Close()
	s.baseURL = server.URL

	err := s.Start(context.Background(), func(alert *domain.Alert) {})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}
