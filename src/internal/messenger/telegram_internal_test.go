package messenger

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func TestTelegramStartStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// getUpdates will be called in pollLoop; return empty to keep it quiet.
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "result": []interface{}{}})
	}))
	defer server.Close()

	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.baseURL = server.URL

	err := tg.Start(context.Background(), func(alert *domain.Alert) {})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if tg.handler == nil {
		t.Error("expected handler to be set after Start")
	}

	if err := tg.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

	// Double Stop should be safe.
	if err := tg.Stop(context.Background()); err != nil {
		t.Fatalf("double Stop returned error: %v", err)
	}
}

func TestTelegramHandleUpdate_NilMessage(t *testing.T) {
	called := false
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.handler = func(alert *domain.Alert) {
		called = true
	}

	tg.handleUpdate(telegramUpdate{UpdateID: 1, Message: nil})

	if called {
		t.Error("expected handler NOT to be called for nil message")
	}
}

func TestTelegramHandleUpdate_SkipsBotMessages(t *testing.T) {
	called := false
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.handler = func(alert *domain.Alert) {
		called = true
	}

	tg.handleUpdate(telegramUpdate{
		UpdateID: 1,
		Message: &telegramMessage{
			MessageID: 1,
			Chat:      telegramChat{ID: -100},
			Text:      "bot message",
			From:      &telegramUser{ID: 999, IsBot: true},
		},
	})

	if called {
		t.Error("expected handler NOT to be called for bot messages")
	}
}

func TestTelegramHandleUpdate_ChatFilter(t *testing.T) {
	called := false
	tg := NewTelegram("token123", -100, []int64{-100, -200}, "HTML", testLogger())
	tg.handler = func(alert *domain.Alert) {
		called = true
	}

	tg.handleUpdate(telegramUpdate{
		UpdateID: 1,
		Message: &telegramMessage{
			MessageID: 1,
			Chat:      telegramChat{ID: -999}, // Not in listen list.
			Text:      "test",
			From:      &telegramUser{ID: 123, IsBot: false},
		},
	})

	if called {
		t.Error("expected handler NOT to be called for chat not in listen list")
	}
}

func TestTelegramHandleUpdate_NilHandler(t *testing.T) {
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	// handler is nil

	// Should not panic.
	tg.handleUpdate(telegramUpdate{
		UpdateID: 1,
		Message: &telegramMessage{
			MessageID: 1,
			Chat:      telegramChat{ID: -100},
			Text:      "test",
			From:      &telegramUser{ID: 123, IsBot: false},
		},
	})
}

func TestTelegramHandleCommand(t *testing.T) {
	var receivedAlert *domain.Alert
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.handler = func(alert *domain.Alert) {
		receivedAlert = alert
	}

	msg := &telegramMessage{
		MessageID: 42,
		Chat:      telegramChat{ID: -100},
		Text:      "/reanalyze server-01",
		From:      &telegramUser{ID: 123, IsBot: false},
		Entities:  []telegramEntity{{Type: "bot_command", Offset: 0, Length: 10}},
		ReplyToMessage: &telegramMessage{
			MessageID: 41,
			Text:      "Original alert text",
		},
	}

	tg.handleCommand(msg, "/reanalyze", "server-01")

	if receivedAlert == nil {
		t.Fatal("expected handler to be called")
	}
	if receivedAlert.Source != "telegram" {
		t.Errorf("expected source 'telegram', got %q", receivedAlert.Source)
	}
	if receivedAlert.Name != "telegram-command" {
		t.Errorf("expected name 'telegram-command', got %q", receivedAlert.Name)
	}
	if receivedAlert.UserCommand != "/reanalyze server-01" {
		t.Errorf("expected UserCommand '/reanalyze server-01', got %q", receivedAlert.UserCommand)
	}
	if receivedAlert.RawText != "Original alert text" {
		t.Errorf("expected RawText from reply, got %q", receivedAlert.RawText)
	}
	if receivedAlert.ReplyTarget.Channel != "-100" {
		t.Errorf("expected Channel '-100', got %q", receivedAlert.ReplyTarget.Channel)
	}
	if receivedAlert.ReplyTarget.ThreadID != "41" {
		t.Errorf("expected ThreadID '41', got %q", receivedAlert.ReplyTarget.ThreadID)
	}
}

func TestTelegramHandleCommand_NoReply(t *testing.T) {
	var receivedAlert *domain.Alert
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.handler = func(alert *domain.Alert) {
		receivedAlert = alert
	}

	msg := &telegramMessage{
		MessageID: 42,
		Chat:      telegramChat{ID: -100},
		Text:      "/status",
		From:      &telegramUser{ID: 123, IsBot: false},
		Entities:  []telegramEntity{{Type: "bot_command", Offset: 0, Length: 7}},
	}

	tg.handleCommand(msg, "/status", "")

	if receivedAlert == nil {
		t.Fatal("expected handler to be called")
	}
	if receivedAlert.RawText != "" {
		t.Errorf("expected empty RawText when no reply, got %q", receivedAlert.RawText)
	}
	if receivedAlert.UserCommand != "/status" {
		t.Errorf("expected UserCommand '/status', got %q", receivedAlert.UserCommand)
	}
	if receivedAlert.ReplyTarget.ThreadID != "" {
		t.Errorf("expected empty ThreadID when no reply, got %q", receivedAlert.ReplyTarget.ThreadID)
	}
}

func TestTelegramHandleReply(t *testing.T) {
	var receivedAlert *domain.Alert
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.handler = func(alert *domain.Alert) {
		receivedAlert = alert
	}

	msg := &telegramMessage{
		MessageID: 43,
		Chat:      telegramChat{ID: -200},
		Text:      "What caused this?",
		From:      &telegramUser{ID: 123, IsBot: false},
		ReplyToMessage: &telegramMessage{
			MessageID: 40,
			Text:      "Alert: CPU is at 95%",
		},
	}

	tg.handleReply(msg)

	if receivedAlert == nil {
		t.Fatal("expected handler to be called")
	}
	if receivedAlert.Source != "telegram" {
		t.Errorf("expected source 'telegram', got %q", receivedAlert.Source)
	}
	if receivedAlert.Name != "telegram-reply" {
		t.Errorf("expected name 'telegram-reply', got %q", receivedAlert.Name)
	}
	if receivedAlert.RawText != "Alert: CPU is at 95%" {
		t.Errorf("expected RawText from reply message, got %q", receivedAlert.RawText)
	}
	if receivedAlert.UserCommand != "What caused this?" {
		t.Errorf("expected UserCommand from message text, got %q", receivedAlert.UserCommand)
	}
	if receivedAlert.ReplyTarget.Channel != "-200" {
		t.Errorf("expected Channel '-200', got %q", receivedAlert.ReplyTarget.Channel)
	}
	if receivedAlert.ReplyTarget.ThreadID != "40" {
		t.Errorf("expected ThreadID '40', got %q", receivedAlert.ReplyTarget.ThreadID)
	}
}

func TestTelegramHandleUpdate_CommandExecution(t *testing.T) {
	var receivedAlert *domain.Alert
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.handler = func(alert *domain.Alert) {
		receivedAlert = alert
	}

	tg.handleUpdate(telegramUpdate{
		UpdateID: 5,
		Message: &telegramMessage{
			MessageID: 50,
			Chat:      telegramChat{ID: -100},
			Text:      "/analyze host-01",
			From:      &telegramUser{ID: 123, IsBot: false},
			Entities:  []telegramEntity{{Type: "bot_command", Offset: 0, Length: 8}},
		},
	})

	if receivedAlert == nil {
		t.Fatal("expected handler to be called for command")
	}
	if receivedAlert.UserCommand != "/analyze host-01" {
		t.Errorf("expected UserCommand '/analyze host-01', got %q", receivedAlert.UserCommand)
	}
}

func TestTelegramHandleUpdate_ReplyExecution(t *testing.T) {
	var receivedAlert *domain.Alert
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.handler = func(alert *domain.Alert) {
		receivedAlert = alert
	}

	tg.handleUpdate(telegramUpdate{
		UpdateID: 6,
		Message: &telegramMessage{
			MessageID: 60,
			Chat:      telegramChat{ID: -100},
			Text:      "Explain this alert",
			From:      &telegramUser{ID: 123, IsBot: false},
			ReplyToMessage: &telegramMessage{
				MessageID: 55,
				Text:      "Alert: Disk full",
			},
		},
	})

	if receivedAlert == nil {
		t.Fatal("expected handler to be called for reply")
	}
	if receivedAlert.Name != "telegram-reply" {
		t.Errorf("expected name 'telegram-reply', got %q", receivedAlert.Name)
	}
}

func TestTelegramResolveTarget_NoReplyTarget(t *testing.T) {
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())

	alert := &domain.Alert{Name: "Test"}
	chatID, replyTo := tg.resolveTarget(alert)

	if chatID != -100 {
		t.Errorf("expected default chat -100, got %d", chatID)
	}
	if replyTo != 0 {
		t.Errorf("expected 0 replyTo, got %d", replyTo)
	}
}

func TestTelegramResolveTarget_DifferentMessenger(t *testing.T) {
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())

	alert := &domain.Alert{
		Name: "Test",
		ReplyTarget: &domain.ReplyTarget{
			Messenger: "slack",
			Channel:   "C123",
		},
	}
	chatID, _ := tg.resolveTarget(alert)

	if chatID != -100 {
		t.Errorf("expected default chat for non-telegram messenger, got %d", chatID)
	}
}

func TestTelegramResolveTarget_InvalidChannel(t *testing.T) {
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())

	alert := &domain.Alert{
		Name: "Test",
		ReplyTarget: &domain.ReplyTarget{
			Messenger: "telegram",
			Channel:   "not-a-number",
		},
	}
	chatID, _ := tg.resolveTarget(alert)

	if chatID != -100 {
		t.Errorf("expected default chat for invalid channel, got %d", chatID)
	}
}

func TestTelegramGetUpdates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/getUpdates" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"result": []map[string]interface{}{
				{
					"update_id": 100,
					"message": map[string]interface{}{
						"message_id": 1,
						"chat":       map[string]interface{}{"id": -100},
						"text":       "hello",
					},
				},
			},
		})
	}))
	defer server.Close()

	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.baseURL = server.URL

	updates, err := tg.getUpdates(context.Background())
	if err != nil {
		t.Fatalf("getUpdates failed: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].UpdateID != 100 {
		t.Errorf("expected UpdateID 100, got %d", updates[0].UpdateID)
	}
}

func TestTelegramGetUpdates_NotOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false})
	}))
	defer server.Close()

	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.baseURL = server.URL

	_, err := tg.getUpdates(context.Background())
	if err == nil {
		t.Error("expected error when getUpdates returns ok=false")
	}
}

func TestTelegramSendError_NoLeak(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.baseURL = server.URL

	alert := &domain.Alert{Name: "CacheAlert"}

	tg.SendError(context.Background(), alert, fmt.Errorf("connection to %s failed", "redis://admin:secret@host:6379"))

	text := receivedBody["text"].(string)
	if contains(text, "secret") {
		t.Error("error message should not contain sensitive details")
	}
	if !contains(text, "internal error occurred") {
		t.Error("expected sanitized error message")
	}
}

func TestTelegramSendError_MarkdownMode(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	tg := NewTelegram("token123", -100, nil, "Markdown", testLogger())
	tg.baseURL = server.URL

	alert := &domain.Alert{Name: "TestAlert"}
	tg.SendError(context.Background(), alert, fmt.Errorf("some error"))

	text := receivedBody["text"].(string)
	if !contains(text, "*Error analyzing alert*") {
		t.Error("expected Markdown formatting for error message")
	}
}

func TestTelegramDoWithRetry_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":         false,
			"parameters": map[string]interface{}{"retry_after": 60},
		})
	}))
	defer server.Close()

	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.baseURL = server.URL

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := tg.doWithRetry(ctx, server.URL+"/test", []byte(`{}`))
	if err == nil {
		t.Error("expected error after context timeout during retries")
	}
}

func TestFormatTelegramAlert_HTML(t *testing.T) {
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

	text := formatTelegramAlert(alert, "HTML")

	if !contains(text, "<b>[FIRING] HighCPU</b>") {
		t.Errorf("expected HTML bold status and name, got: %s", text)
	}
	if !contains(text, "<code>critical</code>") {
		t.Errorf("expected severity in code tag, got: %s", text)
	}
	if !contains(text, "Env: <code>us-east</code>") {
		t.Errorf("expected cluster env, got: %s", text)
	}
	if !contains(text, "Target: <code>pod: nginx-abc</code>") {
		t.Errorf("expected target, got: %s", text)
	}
	if !contains(text, "CPU usage is above 90%") {
		t.Errorf("expected summary, got: %s", text)
	}
	if !contains(text, "namespace=prod") {
		t.Errorf("expected labels context, got: %s", text)
	}
}

func TestFormatTelegramAlert_Markdown(t *testing.T) {
	alert := &domain.Alert{
		Name:     "DiskFull",
		Severity: domain.SeverityWarning,
		Status:   domain.StatusResolved,
		Labels:   map[string]string{"host": "node-1"},
	}

	text := formatTelegramAlert(alert, "Markdown")

	if !contains(text, "*[RESOLVED] DiskFull*") {
		t.Errorf("expected Markdown status and name, got: %s", text)
	}
	if !contains(text, "`warning`") {
		t.Errorf("expected severity in backticks, got: %s", text)
	}
	if !contains(text, "Target: `host: node-1`") {
		t.Errorf("expected target, got: %s", text)
	}
}

func TestFormatTelegramAlert_NoOptionalFields(t *testing.T) {
	alert := &domain.Alert{
		Name:   "SimpleAlert",
		Status: domain.StatusFiring,
		Labels: map[string]string{},
	}

	text := formatTelegramAlert(alert, "HTML")

	if !contains(text, "[FIRING] SimpleAlert") {
		t.Errorf("expected status and name, got: %s", text)
	}
	if contains(text, "Level:") {
		t.Error("should not contain Level when severity is empty")
	}
	if contains(text, "Target:") {
		t.Error("should not contain Target when no target labels")
	}
}

func TestFormatTelegramAnalysisRich_HTML(t *testing.T) {
	result := &domain.AnalysisResult{
		Text: "Investigation findings.",
		ToolsTrace: []domain.ToolTraceEntry{
			{Name: "prometheus", Success: true},
			{Name: "loki", Success: false},
		},
	}

	text := formatTelegramAnalysisRich(result, "HTML")

	if !contains(text, "<b>SherlockOps Investigation</b>") {
		t.Errorf("expected HTML investigation header, got: %s", text)
	}
	if !contains(text, "Investigation findings.") {
		t.Error("expected analysis text")
	}
	if !contains(text, "<i>") {
		t.Error("expected italic tools footer in HTML")
	}
	if !contains(text, "prometheus") {
		t.Error("expected tools trace")
	}
}

func TestFormatTelegramAnalysisRich_Markdown(t *testing.T) {
	result := &domain.AnalysisResult{
		Text: "All clear.",
	}

	text := formatTelegramAnalysisRich(result, "Markdown")

	if !contains(text, "*SherlockOps Investigation*") {
		t.Errorf("expected Markdown investigation header, got: %s", text)
	}
	if contains(text, "Tools:") {
		t.Error("should not contain tools footer when no tools")
	}
}

func TestTelegramSendAlert(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":     true,
			"result": map[string]interface{}{"message_id": 42},
		})
	}))
	defer server.Close()

	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.baseURL = server.URL

	alert := &domain.Alert{
		Name:     "HighCPU",
		Severity: domain.SeverityCritical,
		Status:   domain.StatusFiring,
		Labels:   map[string]string{"pod": "nginx-abc"},
	}

	ref, err := tg.SendAlert(context.Background(), alert)
	if err != nil {
		t.Fatalf("SendAlert failed: %v", err)
	}
	if ref == nil {
		t.Fatal("expected non-nil MessageRef")
	}
	if ref.Messenger != "telegram" {
		t.Errorf("expected messenger 'telegram', got %q", ref.Messenger)
	}
	if ref.Channel != "-100" {
		t.Errorf("expected channel '-100', got %q", ref.Channel)
	}
	if ref.MessageID != "42" {
		t.Errorf("expected MessageID '42', got %q", ref.MessageID)
	}
}

func TestTelegramSendAnalysisReply(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.baseURL = server.URL

	ref := &domain.MessageRef{
		Messenger: "telegram",
		Channel:   "-100",
		MessageID: "42",
		Alert: &domain.Alert{
			Name:     "HighCPU",
			Severity: domain.SeverityCritical,
			Status:   domain.StatusFiring,
			Labels:   map[string]string{},
		},
	}
	result := &domain.AnalysisResult{
		Text:      "Analysis result.",
		ToolsUsed: []string{"prometheus"},
	}

	err := tg.SendAnalysisReply(context.Background(), ref, result)
	if err != nil {
		t.Fatalf("SendAnalysisReply failed: %v", err)
	}
}

func TestTelegramResolveTarget_ChannelOverride(t *testing.T) {
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())

	alert := &domain.Alert{
		Name:             "Test",
		ChannelOverrides: map[string]string{"telegram": "-200"},
	}
	chatID, _ := tg.resolveTarget(alert)

	if chatID != -200 {
		t.Errorf("expected chat ID -200, got %d", chatID)
	}
}
