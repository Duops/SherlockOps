package messenger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

// TeamsMessenger implements domain.Messenger for Microsoft Teams.
// It supports two modes:
//   - Incoming Webhook (simple): set webhookURL, no auth needed.
//   - Bot Framework (full): set tenantID, clientID, clientSecret for OAuth2 auth,
//     message sending via Bot Framework API, and listener mode via HTTP endpoint.
type TeamsMessenger struct {
	tenantID       string
	clientID       string
	clientSecret   string
	botID          string
	webhookURL     string // incoming webhook URL (simple mode)
	listenPort     int
	accessToken    string
	tokenExpiry    time.Time
	mu             sync.Mutex
	defaultTeam    string
	defaultChannel string
	client         *http.Client
	handler        func(alert *domain.Alert)
	logger         *slog.Logger
	cancel         context.CancelFunc
	server         *http.Server

	// Overridable for testing.
	tokenURL       string
	botFrameworkURL string
}

// NewTeams creates a new TeamsMessenger.
func NewTeams(tenantID, clientID, clientSecret, webhookURL, defaultTeam, defaultChannel string, listenPort int, logger *slog.Logger) *TeamsMessenger {
	if listenPort <= 0 {
		listenPort = 3978
	}
	return &TeamsMessenger{
		tenantID:        tenantID,
		clientID:        clientID,
		clientSecret:    clientSecret,
		webhookURL:      webhookURL,
		defaultTeam:     defaultTeam,
		defaultChannel:  defaultChannel,
		listenPort:      listenPort,
		client:          &http.Client{Timeout: 30 * time.Second},
		logger:          logger,
		tokenURL:        fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID),
		botFrameworkURL: "https://smba.trafficmanager.net/teams",
	}
}

func (t *TeamsMessenger) Name() string {
	return "teams"
}

// Start begins listening for incoming Bot Framework activities if in Bot Framework mode.
// In webhook-only mode this is a no-op.
func (t *TeamsMessenger) Start(ctx context.Context, handler func(alert *domain.Alert)) error {
	t.handler = handler

	if !t.isBotFrameworkMode() {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	t.mu.Lock()
	t.cancel = cancel
	t.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/teams", t.handleBotFrameworkActivity)

	t.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", t.listenPort),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		t.logger.Info("teams bot framework listener started", slog.String("addr", t.server.Addr))
		if err := t.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.logger.Error("teams listener error", slog.String("error", err.Error()))
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = t.server.Shutdown(shutdownCtx)
	}()

	return nil
}

// Stop gracefully shuts down the messenger.
func (t *TeamsMessenger) Stop(_ context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	return nil
}

// SendAlert posts the raw alert as an Adaptive Card and returns a MessageRef (Phase 1).
func (t *TeamsMessenger) SendAlert(ctx context.Context, alert *domain.Alert) (*domain.MessageRef, error) {
	card := buildAlertCard(alert)
	activityID, err := t.sendCardWithID(ctx, alert, card)
	if err != nil {
		return nil, err
	}
	conversationID := t.resolveConversationID(alert)
	return &domain.MessageRef{
		Messenger: "teams",
		Channel:   conversationID,
		MessageID: activityID,
		Alert:     alert,
	}, nil
}

// SendAnalysisReply updates the original alert card to include the analysis (Phase 2).
func (t *TeamsMessenger) SendAnalysisReply(ctx context.Context, ref *domain.MessageRef, result *domain.AnalysisResult) error {
	card := buildAnalysisCard(ref.Alert, result)
	return t.updateCard(ctx, ref, card)
}

// SendAnalysis posts an analysis result as an Adaptive Card.
func (t *TeamsMessenger) SendAnalysis(ctx context.Context, alert *domain.Alert, result *domain.AnalysisResult) error {
	card := buildAnalysisCard(alert, result)
	return t.sendCard(ctx, alert, card)
}

// SendError posts an error message as an Adaptive Card.
// Only a generic message is sent; the full error is logged server-side
// to avoid leaking internal details.
func (t *TeamsMessenger) SendError(ctx context.Context, alert *domain.Alert, err error) error {
	t.logger.Error("alert analysis failed", "alert", alert.Name, "error", err)

	card := buildErrorCard(alert)
	return t.sendCard(ctx, alert, card)
}

// isBotFrameworkMode returns true if Bot Framework credentials are configured.
func (t *TeamsMessenger) isBotFrameworkMode() bool {
	return t.tenantID != "" && t.clientID != "" && t.clientSecret != ""
}

// sendCard sends an Adaptive Card via webhook or Bot Framework API.
func (t *TeamsMessenger) sendCard(ctx context.Context, alert *domain.Alert, card adaptiveCard) error {
	_, err := t.sendCardWithID(ctx, alert, card)
	return err
}

// sendCardWithID sends an Adaptive Card and returns the activity ID from the response.
func (t *TeamsMessenger) sendCardWithID(ctx context.Context, alert *domain.Alert, card adaptiveCard) (string, error) {
	payload := teamsMessage{
		Type: "message",
		Attachments: []teamsAttachment{
			{
				ContentType: "application/vnd.microsoft.card.adaptive",
				Content:     card,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal teams message: %w", err)
	}

	if t.webhookURL != "" {
		respBody, err := t.postWithRetry(ctx, t.webhookURL, body, false)
		if err != nil {
			return "", err
		}
		return t.extractActivityID(respBody), nil
	}

	if t.isBotFrameworkMode() {
		conversationID := t.resolveConversationID(alert)
		if conversationID == "" {
			return "", fmt.Errorf("teams: no conversation ID available for alert %s", alert.Name)
		}
		url := fmt.Sprintf("%s/v3/conversations/%s/activities", t.botFrameworkURL, conversationID)
		respBody, err := t.postWithRetry(ctx, url, body, true)
		if err != nil {
			return "", err
		}
		return t.extractActivityID(respBody), nil
	}

	return "", fmt.Errorf("teams: neither webhook URL nor Bot Framework credentials configured")
}

// extractActivityID parses the activity ID from a Bot Framework response.
func (t *TeamsMessenger) extractActivityID(respBody []byte) string {
	var resp struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(respBody, &resp) == nil {
		return resp.ID
	}
	return ""
}

// updateCard updates an existing message via Bot Framework API.
func (t *TeamsMessenger) updateCard(ctx context.Context, ref *domain.MessageRef, card adaptiveCard) error {
	payload := teamsMessage{
		Type: "message",
		Attachments: []teamsAttachment{
			{
				ContentType: "application/vnd.microsoft.card.adaptive",
				Content:     card,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal teams message: %w", err)
	}

	if t.webhookURL != "" {
		// Incoming webhooks do not support updating messages; post a new one.
		_, err := t.postWithRetry(ctx, t.webhookURL, body, false)
		return err
	}

	if t.isBotFrameworkMode() && ref.Channel != "" && ref.MessageID != "" {
		url := fmt.Sprintf("%s/v3/conversations/%s/activities/%s", t.botFrameworkURL, ref.Channel, ref.MessageID)
		_, err := t.putWithRetry(ctx, url, body)
		return err
	}

	// Fallback: post as new message.
	_, err = t.postWithRetry(ctx, t.webhookURL, body, false)
	return err
}

// resolveConversationID extracts the conversation ID from the alert's reply target.
func (t *TeamsMessenger) resolveConversationID(alert *domain.Alert) string {
	if alert.ReplyTarget != nil && alert.ReplyTarget.Messenger == "teams" {
		return alert.ReplyTarget.Channel
	}
	if t.defaultChannel != "" {
		return t.defaultChannel
	}
	return ""
}

// ensureToken obtains or refreshes the Bot Framework OAuth2 token.
func (t *TeamsMessenger) ensureToken(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.accessToken != "" && time.Now().Before(t.tokenExpiry.Add(-60*time.Second)) {
		return nil
	}

	form := fmt.Sprintf(
		"grant_type=client_credentials&client_id=%s&client_secret=%s&scope=%s",
		t.clientID,
		t.clientSecret,
		"https://api.botframework.com/.default",
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.tokenURL, strings.NewReader(form))
	if err != nil {
		return fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token exchange failed: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return fmt.Errorf("decode token response: %w", err)
	}

	t.accessToken = tokenResp.AccessToken
	t.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	return nil
}

// postWithRetry sends a POST request with retry on 429.
// It returns the raw response body on success.
func (t *TeamsMessenger) postWithRetry(ctx context.Context, url string, body []byte, useAuth bool) ([]byte, error) {
	return t.doRequestWithRetry(ctx, http.MethodPost, url, body, useAuth)
}

// putWithRetry sends a PUT request (for updating activities) with retry on 429.
func (t *TeamsMessenger) putWithRetry(ctx context.Context, url string, body []byte) ([]byte, error) {
	return t.doRequestWithRetry(ctx, http.MethodPut, url, body, true)
}

// doRequestWithRetry sends an HTTP request with retry on 429.
func (t *TeamsMessenger) doRequestWithRetry(ctx context.Context, method, url string, body []byte, useAuth bool) ([]byte, error) {
	const maxRetries = 5
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if useAuth {
			if err := t.ensureToken(ctx); err != nil {
				return nil, fmt.Errorf("ensure token: %w", err)
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")

		if useAuth {
			t.mu.Lock()
			token := t.accessToken
			t.mu.Unlock()
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := t.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
			t.logger.Warn("rate limited by Teams, retrying",
				slog.Int("attempt", attempt+1),
				slog.Duration("backoff", backoff),
			)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				continue
			}
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("teams API error: status=%d body=%s", resp.StatusCode, string(respBody))
		}

		return respBody, nil
	}
	return nil, fmt.Errorf("teams API: max retries exceeded")
}

// handleBotFrameworkActivity processes incoming Bot Framework activities.
func (t *TeamsMessenger) handleBotFrameworkActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var activity botActivity
	if err := json.Unmarshal(body, &activity); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if activity.Type != "message" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if t.handler == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Strip @mention tags from the text.
	text := stripMentions(activity.Text, activity.Entities)

	alert := &domain.Alert{
		Source:     "teams",
		Name:       "teams-mention",
		RawText:    text,
		ReceivedAt: time.Now(),
		ReplyTarget: &domain.ReplyTarget{
			Messenger: "teams",
			Channel:   activity.Conversation.ID,
			ThreadID:  activity.ID,
		},
		UserCommand: text,
	}

	t.handler(alert)
	w.WriteHeader(http.StatusOK)
}

// stripMentions removes <at>...</at> mention tags from the text.
func stripMentions(text string, entities []botEntity) string {
	for _, e := range entities {
		if e.Type == "mention" && e.Mentioned.Name != "" {
			text = strings.ReplaceAll(text, fmt.Sprintf("<at>%s</at>", e.Mentioned.Name), "")
		}
	}
	return strings.TrimSpace(text)
}

// severityEmoji returns an emoji for the alert severity.
func severityEmoji(severity domain.Severity) string {
	switch severity {
	case domain.SeverityCritical:
		return "\U0001F534" // red circle
	case domain.SeverityWarning:
		return "\U0001F7E0" // orange circle
	case domain.SeverityInfo:
		return "\U0001F535" // blue circle
	default:
		return "\u26AA" // white circle
	}
}

// buildAlertCard constructs an Adaptive Card for a raw alert (Phase 1).
func buildAlertCard(alert *domain.Alert) adaptiveCard {
	emoji := severityEmoji(alert.Severity)
	status := "FIRING"
	if alert.Status == domain.StatusResolved {
		status = "RESOLVED"
	}
	title := fmt.Sprintf("%s [%s] %s", emoji, status, alert.Name)

	bodyItems := []cardElement{
		{Type: "TextBlock", Text: title, Weight: "Bolder", Size: "Medium"},
	}

	if alert.Severity != "" {
		bodyItems = append(bodyItems, cardElement{
			Type:     "TextBlock",
			Text:     fmt.Sprintf("Severity: %s | Status: %s", alert.Severity, alert.Status),
			IsSubtle: true,
		})
	}

	if summary := alert.Annotations["summary"]; summary != "" {
		bodyItems = append(bodyItems, cardElement{
			Type: "TextBlock",
			Text: summary,
			Wrap: true,
		})
	}

	return adaptiveCard{
		Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
		Type:    "AdaptiveCard",
		Version: "1.4",
		Body:    bodyItems,
	}
}

// buildAnalysisCard constructs an Adaptive Card for an analysis result.
func buildAnalysisCard(alert *domain.Alert, result *domain.AnalysisResult) adaptiveCard {
	emoji := severityEmoji(alert.Severity)
	title := fmt.Sprintf("%s Alert: %s", emoji, alert.Name)

	bodyItems := []cardElement{
		{Type: "TextBlock", Text: title, Weight: "Bolder", Size: "Medium"},
	}

	if alert.Severity != "" {
		bodyItems = append(bodyItems, cardElement{
			Type: "TextBlock",
			Text: fmt.Sprintf("Severity: %s | Status: %s", alert.Severity, alert.Status),
			IsSubtle: true,
		})
	}

	bodyItems = append(bodyItems, cardElement{
		Type: "TextBlock",
		Text: result.Text,
		Wrap: true,
	})

	if trace := formatToolsTraceFromResult(result); trace != "" {
		bodyItems = append(bodyItems, cardElement{
			Type:     "TextBlock",
			Text:     "🛠️ Tools: " + trace,
			IsSubtle: true,
			Size:     "Small",
		})
	}

	return adaptiveCard{
		Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
		Type:    "AdaptiveCard",
		Version: "1.4",
		Body:    bodyItems,
	}
}

// buildErrorCard constructs an Adaptive Card for an error.
func buildErrorCard(alert *domain.Alert) adaptiveCard {
	return adaptiveCard{
		Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
		Type:    "AdaptiveCard",
		Version: "1.4",
		Body: []cardElement{
			{
				Type:   "TextBlock",
				Text:   fmt.Sprintf("\u26A0\uFE0F Error analyzing alert: %s", alert.Name),
				Weight: "Bolder",
				Size:   "Medium",
				Color:  "Attention",
			},
			{
				Type: "TextBlock",
				Text: "An internal error occurred. Please check the server logs.",
				Wrap: true,
			},
		},
	}
}

// --- Wire types ---

type teamsMessage struct {
	Type        string            `json:"type"`
	Attachments []teamsAttachment `json:"attachments"`
}

type teamsAttachment struct {
	ContentType string       `json:"contentType"`
	Content     adaptiveCard `json:"content"`
}

type adaptiveCard struct {
	Schema  string        `json:"$schema"`
	Type    string        `json:"type"`
	Version string        `json:"version"`
	Body    []cardElement `json:"body"`
}

type cardElement struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Weight   string `json:"weight,omitempty"`
	Size     string `json:"size,omitempty"`
	Wrap     bool   `json:"wrap,omitempty"`
	IsSubtle bool   `json:"isSubtle,omitempty"`
	Color    string `json:"color,omitempty"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

type botActivity struct {
	Type         string          `json:"type"`
	ID           string          `json:"id"`
	Text         string          `json:"text"`
	From         botAccount      `json:"from"`
	Conversation botConversation `json:"conversation"`
	Entities     []botEntity     `json:"entities"`
}

type botAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type botConversation struct {
	ID string `json:"id"`
}

type botEntity struct {
	Type      string     `json:"type"`
	Mentioned botAccount `json:"mentioned"`
}
