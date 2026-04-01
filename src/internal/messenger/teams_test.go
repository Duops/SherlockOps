package messenger

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
}

func TestTeamsWebhookSendAnalysis(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			t.Errorf("expected application/json content type, got %s", ct)
		}
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		received = buf
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tm := NewTeams("", "", "", srv.URL, "", "#general", 0, newTestLogger())

	alert := &domain.Alert{
		Name:     "HighCPU",
		Severity: domain.SeverityCritical,
		Status:   domain.StatusFiring,
	}
	result := &domain.AnalysisResult{
		Text:      "CPU usage is above 90% on node-1.",
		ToolsUsed: []string{"prometheus"},
	}

	err := tm.SendAnalysis(context.Background(), alert, result)
	if err != nil {
		t.Fatalf("SendAnalysis failed: %v", err)
	}

	var msg teamsMessage
	if err := json.Unmarshal(received, &msg); err != nil {
		t.Fatalf("failed to unmarshal sent message: %v", err)
	}
	if msg.Type != "message" {
		t.Errorf("expected type 'message', got %q", msg.Type)
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(msg.Attachments))
	}
	if msg.Attachments[0].ContentType != "application/vnd.microsoft.card.adaptive" {
		t.Errorf("unexpected content type: %s", msg.Attachments[0].ContentType)
	}
}

func TestTeamsWebhookSendError(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		received = buf
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tm := NewTeams("", "", "", srv.URL, "", "#general", 0, newTestLogger())

	alert := &domain.Alert{
		Name:     "DiskFull",
		Severity: domain.SeverityWarning,
		Status:   domain.StatusFiring,
	}

	err := tm.SendError(context.Background(), alert, fmt.Errorf("connection timeout"))
	if err != nil {
		t.Fatalf("SendError failed: %v", err)
	}

	var msg teamsMessage
	if err := json.Unmarshal(received, &msg); err != nil {
		t.Fatalf("failed to unmarshal sent message: %v", err)
	}
	if msg.Type != "message" {
		t.Errorf("expected type 'message', got %q", msg.Type)
	}

	card := msg.Attachments[0].Content
	if card.Version != "1.4" {
		t.Errorf("expected card version 1.4, got %s", card.Version)
	}
	if len(card.Body) < 2 {
		t.Fatalf("expected at least 2 body elements, got %d", len(card.Body))
	}
	if !strings.Contains(card.Body[0].Text, "DiskFull") {
		t.Errorf("error card should contain alert name, got %q", card.Body[0].Text)
	}
	// Verify error text does NOT contain the actual error message (no leaking).
	if strings.Contains(card.Body[1].Text, "connection timeout") {
		t.Error("error card should not contain internal error details")
	}
}

func TestAdaptiveCardStructure(t *testing.T) {
	alert := &domain.Alert{
		Name:     "MemoryLeak",
		Severity: domain.SeverityCritical,
		Status:   domain.StatusFiring,
	}
	result := &domain.AnalysisResult{
		Text:      "Memory usage growing unbounded in service-x.",
		ToolsUsed: []string{"prometheus", "loki"},
	}

	card := buildAnalysisCard(alert, result)

	if card.Schema != "http://adaptivecards.io/schemas/adaptive-card.json" {
		t.Errorf("unexpected schema: %s", card.Schema)
	}
	if card.Type != "AdaptiveCard" {
		t.Errorf("unexpected type: %s", card.Type)
	}
	if card.Version != "1.4" {
		t.Errorf("unexpected version: %s", card.Version)
	}

	// Should have: title, severity/status, analysis text, tools used.
	if len(card.Body) < 4 {
		t.Fatalf("expected at least 4 body elements, got %d", len(card.Body))
	}

	// Title should be bold.
	if card.Body[0].Weight != "Bolder" {
		t.Errorf("title should be Bolder, got %q", card.Body[0].Weight)
	}
	if !strings.Contains(card.Body[0].Text, "MemoryLeak") {
		t.Errorf("title should contain alert name, got %q", card.Body[0].Text)
	}

	// Analysis text should wrap.
	analysisBlock := card.Body[2]
	if !analysisBlock.Wrap {
		t.Error("analysis text block should have Wrap=true")
	}
	if analysisBlock.Text != result.Text {
		t.Errorf("analysis text mismatch: got %q", analysisBlock.Text)
	}

	// Tools used.
	toolsBlock := card.Body[3]
	if !strings.Contains(toolsBlock.Text, "prometheus") {
		t.Errorf("tools block should mention prometheus, got %q", toolsBlock.Text)
	}
}

func TestBotFrameworkTokenExchange(t *testing.T) {
	tokenCalled := false
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalled = true
		if r.Method != http.MethodPost {
			t.Errorf("expected POST for token, got %s", r.Method)
		}
		ct := r.Header.Get("Content-Type")
		if ct != "application/x-www-form-urlencoded" {
			t.Errorf("expected form content type, got %s", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken: "test-token-abc",
			ExpiresIn:   3600,
			TokenType:   "Bearer",
		})
	}))
	defer tokenSrv.Close()

	botSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token-abc" {
			t.Errorf("expected Bearer test-token-abc, got %s", auth)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer botSrv.Close()

	tm := NewTeams("test-tenant", "test-client", "test-secret", "", "", "conv-123", 0, newTestLogger())
	tm.tokenURL = tokenSrv.URL
	tm.botFrameworkURL = botSrv.URL

	alert := &domain.Alert{
		Name:     "TestAlert",
		Severity: domain.SeverityInfo,
		Status:   domain.StatusFiring,
	}
	result := &domain.AnalysisResult{
		Text: "All clear.",
	}

	err := tm.SendAnalysis(context.Background(), alert, result)
	if err != nil {
		t.Fatalf("SendAnalysis via Bot Framework failed: %v", err)
	}
	if !tokenCalled {
		t.Error("token endpoint was not called")
	}
}

func TestRateLimitHandling(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tm := NewTeams("", "", "", srv.URL, "", "#general", 0, newTestLogger())

	alert := &domain.Alert{
		Name:     "RateLimitTest",
		Severity: domain.SeverityInfo,
		Status:   domain.StatusFiring,
	}
	result := &domain.AnalysisResult{
		Text: "Test result.",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := tm.SendAnalysis(ctx, alert, result)
	if err != nil {
		t.Fatalf("SendAnalysis should have succeeded after retries: %v", err)
	}
	if attempts < 3 {
		t.Errorf("expected at least 3 attempts (2 rate-limited + 1 success), got %d", attempts)
	}
}

func TestStripMentions(t *testing.T) {
	text := "<at>AlertBot</at> analyze this alert"
	entities := []botEntity{
		{Type: "mention", Mentioned: botAccount{Name: "AlertBot"}},
	}
	result := stripMentions(text, entities)
	expected := "analyze this alert"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestBotFrameworkActivityHandler(t *testing.T) {
	var receivedAlert *domain.Alert

	tm := NewTeams("tenant", "client", "secret", "", "", "", 0, newTestLogger())
	tm.handler = func(alert *domain.Alert) {
		receivedAlert = alert
	}

	activity := botActivity{
		Type: "message",
		ID:   "activity-1",
		Text: "<at>AlertBot</at> check cpu",
		From: botAccount{ID: "user-1", Name: "John"},
		Conversation: botConversation{
			ID: "conv-abc",
		},
		Entities: []botEntity{
			{Type: "mention", Mentioned: botAccount{ID: "bot-1", Name: "AlertBot"}},
		},
	}
	body, _ := json.Marshal(activity)

	req := httptest.NewRequest(http.MethodPost, "/webhook/teams", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	tm.handleBotFrameworkActivity(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if receivedAlert == nil {
		t.Fatal("handler was not called")
	}
	if receivedAlert.UserCommand != "check cpu" {
		t.Errorf("expected command 'check cpu', got %q", receivedAlert.UserCommand)
	}
	if receivedAlert.ReplyTarget.Channel != "conv-abc" {
		t.Errorf("expected channel 'conv-abc', got %q", receivedAlert.ReplyTarget.Channel)
	}
}

func TestName(t *testing.T) {
	tm := NewTeams("", "", "", "", "", "", 0, newTestLogger())
	if tm.Name() != "teams" {
		t.Errorf("expected 'teams', got %q", tm.Name())
	}
}

func TestBuildAlertCard(t *testing.T) {
	alert := &domain.Alert{
		Name:     "HighCPU",
		Severity: domain.SeverityCritical,
		Status:   domain.StatusFiring,
		Annotations: map[string]string{
			"summary": "CPU usage is above 90%",
		},
	}

	card := buildAlertCard(alert)

	if card.Type != "AdaptiveCard" {
		t.Errorf("expected type AdaptiveCard, got %q", card.Type)
	}
	if card.Version != "1.4" {
		t.Errorf("expected version 1.4, got %q", card.Version)
	}
	if len(card.Body) < 3 {
		t.Fatalf("expected at least 3 body elements (title, severity, summary), got %d", len(card.Body))
	}
	if !strings.Contains(card.Body[0].Text, "[FIRING] HighCPU") {
		t.Errorf("expected title to contain status and name, got %q", card.Body[0].Text)
	}
	if card.Body[0].Weight != "Bolder" {
		t.Errorf("expected title weight Bolder, got %q", card.Body[0].Weight)
	}
}

func TestBuildAlertCard_Resolved(t *testing.T) {
	alert := &domain.Alert{
		Name:   "HighCPU",
		Status: domain.StatusResolved,
	}

	card := buildAlertCard(alert)
	if !strings.Contains(card.Body[0].Text, "[RESOLVED]") {
		t.Errorf("expected RESOLVED in title, got %q", card.Body[0].Text)
	}
}

func TestBuildAlertCard_NoSeverity(t *testing.T) {
	alert := &domain.Alert{
		Name:   "SimpleAlert",
		Status: domain.StatusFiring,
	}

	card := buildAlertCard(alert)
	// Should only have title (no severity block, no summary).
	if len(card.Body) != 1 {
		t.Errorf("expected 1 body element for alert without severity/summary, got %d", len(card.Body))
	}
}

func TestSeverityEmoji(t *testing.T) {
	tests := []struct {
		sev  domain.Severity
		want string
	}{
		{domain.SeverityCritical, "\U0001F534"},
		{domain.SeverityWarning, "\U0001F7E0"},
		{domain.SeverityInfo, "\U0001F535"},
		{domain.Severity(""), "\u26AA"},
		{domain.Severity("other"), "\u26AA"},
	}

	for _, tt := range tests {
		got := severityEmoji(tt.sev)
		if got != tt.want {
			t.Errorf("severityEmoji(%q) = %q, want %q", tt.sev, got, tt.want)
		}
	}
}

func TestTeamsStartStop_WebhookMode(t *testing.T) {
	tm := NewTeams("", "", "", "http://webhook.example.com", "", "#general", 0, newTestLogger())

	err := tm.Start(context.Background(), func(alert *domain.Alert) {})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if err := tm.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	// Double Stop should be safe.
	if err := tm.Stop(context.Background()); err != nil {
		t.Fatalf("double Stop returned error: %v", err)
	}
}

func TestTeamsResolveConversationID(t *testing.T) {
	tests := []struct {
		name    string
		tm      *TeamsMessenger
		alert   *domain.Alert
		want    string
	}{
		{
			name:  "from reply target",
			tm:    NewTeams("", "", "", "", "", "", 0, newTestLogger()),
			alert: &domain.Alert{ReplyTarget: &domain.ReplyTarget{Messenger: "teams", Channel: "conv-123"}},
			want:  "conv-123",
		},
		{
			name:  "from default channel",
			tm:    NewTeams("", "", "", "", "", "default-conv", 0, newTestLogger()),
			alert: &domain.Alert{},
			want:  "default-conv",
		},
		{
			name:  "non-teams reply target falls through to default",
			tm:    NewTeams("", "", "", "", "", "default-conv", 0, newTestLogger()),
			alert: &domain.Alert{ReplyTarget: &domain.ReplyTarget{Messenger: "slack", Channel: "C123"}},
			want:  "default-conv",
		},
		{
			name:  "empty when no default and no reply target",
			tm:    NewTeams("", "", "", "", "", "", 0, newTestLogger()),
			alert: &domain.Alert{},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.tm.resolveConversationID(tt.alert)
			if got != tt.want {
				t.Errorf("resolveConversationID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTeamsSendAlert_Webhook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"id": "act-123"})
	}))
	defer srv.Close()

	tm := NewTeams("", "", "", srv.URL, "", "#general", 0, newTestLogger())

	alert := &domain.Alert{
		Name:     "HighCPU",
		Severity: domain.SeverityCritical,
		Status:   domain.StatusFiring,
	}

	ref, err := tm.SendAlert(context.Background(), alert)
	if err != nil {
		t.Fatalf("SendAlert failed: %v", err)
	}
	if ref == nil {
		t.Fatal("expected non-nil MessageRef")
	}
	if ref.Messenger != "teams" {
		t.Errorf("expected messenger 'teams', got %q", ref.Messenger)
	}
	if ref.MessageID != "act-123" {
		t.Errorf("expected MessageID 'act-123', got %q", ref.MessageID)
	}
}

func TestTeamsSendAnalysisReply_Webhook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tm := NewTeams("", "", "", srv.URL, "", "#general", 0, newTestLogger())

	ref := &domain.MessageRef{
		Messenger: "teams",
		Channel:   "conv-123",
		MessageID: "act-123",
		Alert: &domain.Alert{
			Name:     "HighCPU",
			Severity: domain.SeverityCritical,
			Status:   domain.StatusFiring,
		},
	}
	result := &domain.AnalysisResult{
		Text:      "CPU usage is high.",
		ToolsUsed: []string{"prometheus"},
	}

	err := tm.SendAnalysisReply(context.Background(), ref, result)
	if err != nil {
		t.Fatalf("SendAnalysisReply failed: %v", err)
	}
}

func TestTeamsSendAnalysisReply_BotFramework(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken: "test-token",
			ExpiresIn:   3600,
		})
	}))
	defer tokenSrv.Close()

	botSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer botSrv.Close()

	tm := NewTeams("tenant", "client", "secret", "", "", "", 0, newTestLogger())
	tm.tokenURL = tokenSrv.URL
	tm.botFrameworkURL = botSrv.URL

	ref := &domain.MessageRef{
		Messenger: "teams",
		Channel:   "conv-123",
		MessageID: "act-123",
		Alert: &domain.Alert{
			Name:   "HighCPU",
			Status: domain.StatusFiring,
		},
	}
	result := &domain.AnalysisResult{Text: "Analysis."}

	err := tm.SendAnalysisReply(context.Background(), ref, result)
	if err != nil {
		t.Fatalf("SendAnalysisReply via Bot Framework failed: %v", err)
	}
}

func TestBotFrameworkActivityHandler_MethodNotAllowed(t *testing.T) {
	tm := NewTeams("tenant", "client", "secret", "", "", "", 0, newTestLogger())

	req := httptest.NewRequest(http.MethodGet, "/webhook/teams", nil)
	w := httptest.NewRecorder()

	tm.handleBotFrameworkActivity(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestBotFrameworkActivityHandler_NonMessageType(t *testing.T) {
	tm := NewTeams("tenant", "client", "secret", "", "", "", 0, newTestLogger())
	tm.handler = func(alert *domain.Alert) {
		t.Error("handler should not be called for non-message activities")
	}

	activity := botActivity{Type: "conversationUpdate"}
	body, _ := json.Marshal(activity)
	req := httptest.NewRequest(http.MethodPost, "/webhook/teams", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	tm.handleBotFrameworkActivity(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestBotFrameworkActivityHandler_NilHandler(t *testing.T) {
	tm := NewTeams("tenant", "client", "secret", "", "", "", 0, newTestLogger())
	// handler is nil

	activity := botActivity{Type: "message", Text: "hello"}
	body, _ := json.Marshal(activity)
	req := httptest.NewRequest(http.MethodPost, "/webhook/teams", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	tm.handleBotFrameworkActivity(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestBotFrameworkActivityHandler_InvalidJSON(t *testing.T) {
	tm := NewTeams("tenant", "client", "secret", "", "", "", 0, newTestLogger())

	req := httptest.NewRequest(http.MethodPost, "/webhook/teams", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	tm.handleBotFrameworkActivity(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
