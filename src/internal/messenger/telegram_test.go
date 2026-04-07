package messenger

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

func TestTelegramName(t *testing.T) {
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	if tg.Name() != "telegram" {
		t.Errorf("expected Name() = telegram, got %s", tg.Name())
	}
}

func TestFormatTelegramAnalysis_HTML(t *testing.T) {
	alert := &domain.Alert{
		Name:     "HighMemory",
		Severity: domain.SeverityCritical,
		Status:   domain.StatusFiring,
	}
	result := &domain.AnalysisResult{
		Text:      "Memory usage exceeded threshold.",
		ToolsUsed: []string{"grafana"},
	}

	text := formatTelegramAnalysis(alert, result, "HTML")

	if !contains(text, "<b>Alert Analysis: HighMemory</b>") {
		t.Error("expected HTML bold alert name")
	}
	if !contains(text, "<i>Severity: critical | Status: firing</i>") {
		t.Error("expected HTML italic severity")
	}
	if !contains(text, "Memory usage exceeded threshold.") {
		t.Error("expected analysis text")
	}
	if !contains(text, "<i>Tools used: grafana</i>") {
		t.Error("expected tools used in HTML")
	}
}

func TestFormatTelegramAnalysis_Markdown(t *testing.T) {
	alert := &domain.Alert{
		Name:     "DiskIO",
		Severity: domain.SeverityWarning,
		Status:   domain.StatusFiring,
	}
	result := &domain.AnalysisResult{
		Text: "Disk I/O is saturated.",
	}

	text := formatTelegramAnalysis(alert, result, "Markdown")

	if !contains(text, "*Alert Analysis: DiskIO*") {
		t.Error("expected Markdown bold alert name")
	}
	if !contains(text, "_Severity: warning | Status: firing_") {
		t.Error("expected Markdown italic severity")
	}
}

func TestFormatTelegramAnalysis_NoSeverity(t *testing.T) {
	alert := &domain.Alert{Name: "Simple"}
	result := &domain.AnalysisResult{Text: "OK"}

	text := formatTelegramAnalysis(alert, result, "HTML")
	if contains(text, "Severity") {
		t.Error("should not include severity when empty")
	}
}

func TestTelegramSendAnalysis(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sendMessage" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.baseURL = server.URL

	alert := &domain.Alert{
		Name:     "NetError",
		Severity: domain.SeverityWarning,
		Status:   domain.StatusFiring,
	}
	result := &domain.AnalysisResult{Text: "Network connectivity issue."}

	ctx := context.Background()
	if err := tg.SendAnalysis(ctx, alert, result); err != nil {
		t.Fatalf("SendAnalysis failed: %v", err)
	}

	chatID, ok := receivedBody["chat_id"].(float64)
	if !ok || int64(chatID) != -100 {
		t.Errorf("expected chat_id -100, got %v", receivedBody["chat_id"])
	}
	if receivedBody["parse_mode"] != "HTML" {
		t.Errorf("expected parse_mode HTML, got %v", receivedBody["parse_mode"])
	}
	if _, ok := receivedBody["reply_to_message_id"]; ok {
		t.Error("should not have reply_to_message_id when no reply target")
	}
}

func TestTelegramSendAnalysis_InThread(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.baseURL = server.URL

	alert := &domain.Alert{
		Name: "PodRestart",
		ReplyTarget: &domain.ReplyTarget{
			Messenger: "telegram",
			Channel:   "-200",
			ThreadID:  "42",
		},
	}
	result := &domain.AnalysisResult{Text: "Pod restarted due to OOM."}

	ctx := context.Background()
	if err := tg.SendAnalysis(ctx, alert, result); err != nil {
		t.Fatalf("SendAnalysis failed: %v", err)
	}

	chatID, ok := receivedBody["chat_id"].(float64)
	if !ok || int64(chatID) != -200 {
		t.Errorf("expected chat_id -200, got %v", receivedBody["chat_id"])
	}
	replyID, ok := receivedBody["reply_to_message_id"].(float64)
	if !ok || int64(replyID) != 42 {
		t.Errorf("expected reply_to_message_id 42, got %v", receivedBody["reply_to_message_id"])
	}
}

func TestTelegramSendError(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.baseURL = server.URL

	alert := &domain.Alert{Name: "TestAlert"}
	ctx := context.Background()

	if err := tg.SendError(ctx, alert, fmt.Errorf("LLM timeout")); err != nil {
		t.Fatalf("SendError failed: %v", err)
	}

	text, ok := receivedBody["text"].(string)
	if !ok {
		t.Fatal("expected text in body")
	}
	if !contains(text, "Error analyzing alert") {
		t.Error("expected error prefix")
	}
	if !contains(text, "internal error occurred") {
		t.Error("expected sanitized error message")
	}
}

func TestTelegramRateLimitRetry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":          false,
				"description": "Too Many Requests",
				"parameters":  map[string]interface{}{"retry_after": 1},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.baseURL = server.URL

	alert := &domain.Alert{Name: "RateLimitTest"}
	result := &domain.AnalysisResult{Text: "test"}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := tg.SendAnalysis(ctx, alert, result); err != nil {
		t.Fatalf("expected retry to succeed, got: %v", err)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func TestTelegramAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "description": "chat not found"})
	}))
	defer server.Close()

	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())
	tg.baseURL = server.URL

	alert := &domain.Alert{Name: "TestAlert"}
	result := &domain.AnalysisResult{Text: "test"}

	err := tg.SendAnalysis(context.Background(), alert, result)
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "chat not found") {
		t.Errorf("expected chat not found error, got: %v", err)
	}
}

func TestTelegramIsListenChat(t *testing.T) {
	tg := NewTelegram("token123", -100, []int64{111, 222}, "HTML", testLogger())

	if !tg.isListenChat(111) {
		t.Error("expected 111 to be a listen chat")
	}
	if tg.isListenChat(999) {
		t.Error("expected 999 to not be a listen chat")
	}

	// Empty list means accept all.
	tg2 := NewTelegram("token123", -100, nil, "HTML", testLogger())
	if !tg2.isListenChat(12345) {
		t.Error("expected any chat when listen list is empty")
	}
}

func TestTelegramDefaultParseMode(t *testing.T) {
	tg := NewTelegram("token123", -100, nil, "", testLogger())
	if tg.parseMode != "HTML" {
		t.Errorf("expected default parseMode HTML, got %s", tg.parseMode)
	}
}

func TestTelegramExtractCommand(t *testing.T) {
	tg := NewTelegram("token123", -100, nil, "HTML", testLogger())

	tests := []struct {
		name    string
		msg     *telegramMessage
		wantCmd string
		wantArg string
	}{
		{
			name: "reanalyze command",
			msg: &telegramMessage{
				Text:     "/reanalyze server-01",
				Entities: []telegramEntity{{Type: "bot_command", Offset: 0, Length: 10}},
			},
			wantCmd: "/reanalyze",
			wantArg: "server-01",
		},
		{
			name: "silence command with bot name",
			msg: &telegramMessage{
				Text:     "/silence@mybot 2h",
				Entities: []telegramEntity{{Type: "bot_command", Offset: 0, Length: 14}},
			},
			wantCmd: "/silence",
			wantArg: "2h",
		},
		{
			name: "no command",
			msg: &telegramMessage{
				Text:     "just a message",
				Entities: nil,
			},
			wantCmd: "",
			wantArg: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, arg := tg.extractCommand(tt.msg)
			if cmd != tt.wantCmd {
				t.Errorf("expected cmd %q, got %q", tt.wantCmd, cmd)
			}
			if arg != tt.wantArg {
				t.Errorf("expected arg %q, got %q", tt.wantArg, arg)
			}
		})
	}
}
