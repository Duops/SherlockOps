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
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
	"github.com/gorilla/websocket"
)

// SlackMessenger implements domain.Messenger using raw Slack HTTP API and Socket Mode.
type SlackMessenger struct {
	botToken       string
	appToken       string
	signingSecret  string
	defaultChannel string
	listenChannels []string
	client         *http.Client
	handler        func(alert *domain.Alert)
	logger         *slog.Logger
	cancel         context.CancelFunc
	baseURL        string // overridable for testing
	botUserID      string
	mu             sync.Mutex
}

// NewSlack creates a new SlackMessenger.
func NewSlack(botToken, appToken, signingSecret, defaultChannel string, listenChannels []string, logger *slog.Logger) *SlackMessenger {
	return &SlackMessenger{
		botToken:       botToken,
		appToken:       appToken,
		signingSecret:  signingSecret,
		defaultChannel: defaultChannel,
		listenChannels: listenChannels,
		client:         &http.Client{Timeout: 30 * time.Second},
		logger:         logger,
		baseURL:        "https://slack.com/api",
	}
}

func (s *SlackMessenger) Name() string {
	return "slack"
}

// Start connects to Slack Socket Mode and begins listening for events.
func (s *SlackMessenger) Start(ctx context.Context, handler func(alert *domain.Alert)) error {
	s.handler = handler

	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()

	if err := s.resolveBotUserID(ctx); err != nil {
		s.logger.Warn("failed to resolve bot user ID", slog.String("error", err.Error()))
	}

	// Socket Mode (listener) requires app_token. If not set, run in webhook-only mode.
	if s.appToken == "" {
		s.logger.Info("slack: app_token not set, running in webhook-only mode (no listener)")
		return nil
	}

	go s.socketModeLoop(ctx)
	return nil
}

// Stop gracefully shuts down the messenger.
func (s *SlackMessenger) Stop(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	return nil
}

// SendAlert posts a rich alert to a Slack channel using attachments and returns a MessageRef (Phase 1).
func (s *SlackMessenger) SendAlert(ctx context.Context, alert *domain.Alert) (*domain.MessageRef, error) {
	channel, threadTS := s.resolveTarget(alert)

	status := "FIRING"
	if alert.Status == domain.StatusResolved {
		status = "RESOLVED"
	}

	count := len(alert.GroupedAlerts)
	targetType, targetName := extractTarget(alert)

	// Header line: [STATUS:N] AlertName (N = count for grouped alerts)
	var headerText string
	if count > 1 {
		headerText = fmt.Sprintf("*[%s:%d] %s*", status, count, alert.Name)
	} else {
		headerText = fmt.Sprintf("*[%s] %s*", status, alert.Name)
	}
	if alert.Severity != "" {
		headerText += fmt.Sprintf("\nLevel: `%s`", alert.Severity)
	}
	if env := alert.Labels["cluster"]; env != "" {
		headerText += fmt.Sprintf(" | Env: `%s`", env)
	}

	blocks := []map[string]interface{}{
		{
			"type": "section",
			"text": map[string]interface{}{
				"type": "mrkdwn",
				"text": headerText,
			},
		},
	}

	// Targets: single or grouped.
	if count > 1 {
		var targets strings.Builder
		targets.WriteString("Targets:\n")
		for _, ga := range alert.GroupedAlerts {
			tt, tn := extractTarget(ga)
			instance := ga.Labels["instance"]
			if instance != "" {
				targets.WriteString(fmt.Sprintf("• `%s: %s` (%s)\n", tt, tn, instance))
			} else {
				targets.WriteString(fmt.Sprintf("• `%s: %s`\n", tt, tn))
			}
		}
		blocks = append(blocks, map[string]interface{}{
			"type": "section",
			"text": map[string]interface{}{
				"type": "mrkdwn",
				"text": targets.String(),
			},
		})
	} else if targetName != "" {
		blocks = append(blocks, map[string]interface{}{
			"type": "section",
			"text": map[string]interface{}{
				"type": "mrkdwn",
				"text": fmt.Sprintf("Target: `%s: %s`", targetType, targetName),
			},
		})
	}

	// Summary from annotations.
	if summary := alert.Annotations["summary"]; summary != "" {
		blocks = append(blocks, map[string]interface{}{
			"type": "section",
			"text": map[string]interface{}{
				"type": "mrkdwn",
				"text": summary,
			},
		})
	}

	// Context: secondary labels in small text.
	ctxText := formatLabelsContext(alert.Labels, targetType)
	if ctxText != "" {
		blocks = append(blocks, map[string]interface{}{
			"type": "context",
			"elements": []map[string]interface{}{
				{
					"type": "mrkdwn",
					"text": ctxText,
				},
			},
		})
	}

	// Action buttons: Query, Runbook, Silence.
	var buttons []map[string]interface{}
	// Query button — from generatorURL or grafana_url annotation.
	queryURL := alert.Annotations["generator_url"]
	if queryURL == "" {
		queryURL = alert.Annotations["grafana_url"]
	}
	if queryURL != "" {
		buttons = append(buttons, map[string]interface{}{
			"type": "button",
			"text": map[string]interface{}{"type": "plain_text", "text": "\U0001F4CA Query"},
			"url":  queryURL,
		})
	}
	if runbookURL := alert.Annotations["runbook_url"]; runbookURL != "" {
		buttons = append(buttons, map[string]interface{}{
			"type": "button",
			"text": map[string]interface{}{"type": "plain_text", "text": "\U0001F4D6 Runbook"},
			"url":  runbookURL,
		})
	}
	if silenceURL := alert.Annotations["silence_url"]; silenceURL != "" {
		buttons = append(buttons, map[string]interface{}{
			"type": "button",
			"text": map[string]interface{}{"type": "plain_text", "text": "\U0001F515 Silence"},
			"url":  silenceURL,
		})
	}
	if len(buttons) > 0 {
		blocks = append(blocks, map[string]interface{}{
			"type":     "actions",
			"elements": buttons,
		})
	}

	attachment := map[string]interface{}{
		"color":  severityColor(alert.Severity, alert.Status),
		"blocks": blocks,
	}

	// Fallback text — shown only in push notifications, not in the channel.
	attachment["fallback"] = fmt.Sprintf("[%s] %s", status, alert.Name)

	body := map[string]interface{}{
		"channel":     channel,
		"attachments": []interface{}{attachment},
	}
	if threadTS != "" {
		body["thread_ts"] = threadTS
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal alert message: %w", err)
	}

	respBody, err := s.doWithRetry(ctx, s.baseURL+"/chat.postMessage", jsonBody)
	if err != nil {
		return nil, err
	}

	var result struct {
		TS string `json:"ts"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode ts from response: %w", err)
	}

	return &domain.MessageRef{
		Messenger: "slack",
		Channel:   channel,
		MessageID: result.TS,
		Alert:     alert,
	}, nil
}

// SendAnalysisReply replies in a thread to the original alert message (Phase 2).
func (s *SlackMessenger) SendAnalysisReply(ctx context.Context, ref *domain.MessageRef, result *domain.AnalysisResult) error {
	text := formatSlackAnalysisRich(result)
	_, err := s.postMessage(ctx, ref.Channel, ref.MessageID, text)
	return err
}

// SendAnalysis posts an analysis result to Slack.
func (s *SlackMessenger) SendAnalysis(ctx context.Context, alert *domain.Alert, result *domain.AnalysisResult) error {
	channel, threadTS := s.resolveTarget(alert)
	text := formatSlackAnalysis(alert, result)
	_, err := s.postMessage(ctx, channel, threadTS, text)
	return err
}

// SendError posts an error message to Slack.
// Only a generic message is sent to the channel; the full error is logged server-side
// to avoid leaking internal details (API keys, URLs, stack traces).
func (s *SlackMessenger) SendError(ctx context.Context, alert *domain.Alert, err error) error {
	s.logger.Error("alert analysis failed", "alert", alert.Name, "error", err)

	channel, threadTS := s.resolveTarget(alert)
	text := fmt.Sprintf(":warning: *Error analyzing alert* `%s`\nAn internal error occurred. Please check the server logs.", alert.Name)
	_, postErr := s.postMessage(ctx, channel, threadTS, text)
	return postErr
}

func (s *SlackMessenger) resolveTarget(alert *domain.Alert) (channel, threadTS string) {
	// Check ChannelOverrides from X-Channel-Slack header.
	if ch, ok := alert.ChannelOverrides["slack"]; ok && ch != "" {
		channel = ch
	}
	// Check ReplyTarget (from bot listener mode).
	if alert.ReplyTarget != nil && alert.ReplyTarget.Messenger == "slack" {
		channel = alert.ReplyTarget.Channel
		threadTS = alert.ReplyTarget.ThreadID
	}
	if channel == "" {
		channel = s.defaultChannel
	}
	return channel, threadTS
}

func formatSlackAnalysis(alert *domain.Alert, result *domain.AnalysisResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*Alert Analysis: %s*\n", alert.Name))
	if alert.Severity != "" {
		sb.WriteString(fmt.Sprintf("_Severity: %s | Status: %s_\n", alert.Severity, alert.Status))
	}
	sb.WriteString("\n")
	sb.WriteString(result.Text)
	if len(result.ToolsUsed) > 0 {
		sb.WriteString(fmt.Sprintf("\n\n_Tools used: %s_", strings.Join(result.ToolsUsed, ", ")))
	}
	return sb.String()
}

// formatSlackAnalysisRich wraps LLM analysis text with investigation header and tools footer.
func formatSlackAnalysisRich(result *domain.AnalysisResult) string {
	var sb strings.Builder
	sb.WriteString("\U0001F50D *SherlockOps Investigation*\n")
	if badge := formatCacheBadge(result); badge != "" {
		sb.WriteString(badge)
	}
	sb.WriteString("\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\n")
	sb.WriteString(result.Text)
	sb.WriteString("\n\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")
	trace := formatToolsTraceFromResult(result)
	if trace != "" {
		sb.WriteString(fmt.Sprintf("\n\U0001F6E0\uFE0F _Tools: %s_", trace))
	}
	return sb.String()
}

// postMessage sends a message via chat.postMessage with retry on 429.
// It returns the message timestamp (ts) from the Slack API response.
func (s *SlackMessenger) postMessage(ctx context.Context, channel, threadTS, text string) (string, error) {
	payload := map[string]interface{}{
		"channel": channel,
		"text":    text,
		"mrkdwn":  true,
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal message: %w", err)
	}

	respBody, err := s.doWithRetry(ctx, s.baseURL+"/chat.postMessage", body)
	if err != nil {
		return "", err
	}

	var result struct {
		TS string `json:"ts"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode ts from response: %w", err)
	}
	return result.TS, nil
}

// doWithRetry sends a POST request with bot token auth, retrying on 429.
// It returns the raw response body on success.
func (s *SlackMessenger) doWithRetry(ctx context.Context, url string, body []byte) ([]byte, error) {
	const maxRetries = 5
	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("Authorization", "Bearer "+s.botToken)

		resp, err := s.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
			s.logger.Warn("rate limited by Slack, retrying",
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

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("slack API error: status=%d body=%s", resp.StatusCode, string(respBody))
		}

		var slackResp struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(respBody, &slackResp); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		if !slackResp.OK {
			return nil, fmt.Errorf("slack API error: %s", slackResp.Error)
		}
		return respBody, nil
	}
	return nil, fmt.Errorf("slack API: max retries exceeded")
}

// resolveBotUserID calls auth.test to get the bot's own user ID.
func (s *SlackMessenger) resolveBotUserID(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/auth.test", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.botToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool   `json:"ok"`
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if result.OK {
		s.botUserID = result.UserID
	}
	return nil
}

// socketModeLoop connects to Slack Socket Mode and processes events.
func (s *SlackMessenger) socketModeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		wssURL, err := s.openConnection(ctx)
		if err != nil {
			s.logger.Error("failed to open socket mode connection", slog.String("error", err.Error()))
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		s.listenWebSocket(ctx, wssURL)
	}
}

// openConnection calls apps.connections.open to get a WebSocket URL.
func (s *SlackMessenger) openConnection(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/apps.connections.open", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.appToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		OK  bool   `json:"ok"`
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if !result.OK {
		return "", fmt.Errorf("apps.connections.open failed")
	}
	return result.URL, nil
}

// slackSocketMessage represents a Socket Mode envelope.
type slackSocketMessage struct {
	EnvelopeID string          `json:"envelope_id"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
}

// slackEventPayload represents the event_callback payload.
type slackEventPayload struct {
	Event slackEvent `json:"event"`
}

// slackEvent represents a Slack event.
type slackEvent struct {
	Type    string `json:"type"`
	SubType string `json:"subtype"`
	Text    string `json:"text"`
	User    string `json:"user"`
	Channel string `json:"channel"`
	TS      string `json:"ts"`
	ThreadTS string `json:"thread_ts"`
	BotID   string `json:"bot_id"`
}

// wsReadTimeout is the maximum time to wait for a WebSocket message before
// considering the connection dead. Slack sends pings every ~30s, so 60s is safe.
const wsReadTimeout = 60 * time.Second

// listenWebSocket reads events from the WebSocket connection.
func (s *SlackMessenger) listenWebSocket(ctx context.Context, wssURL string) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wssURL, nil)
	if err != nil {
		s.logger.Error("websocket dial failed", slog.String("error", err.Error()))
		return
	}
	defer conn.Close()

	// Set initial read deadline and refresh on each pong.
	conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return nil
	})

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var msg slackSocketMessage
		if err := conn.ReadJSON(&msg); err != nil {
			s.logger.Warn("websocket read error", slog.String("error", err.Error()))
			return
		}

		// Refresh read deadline after each successful message.
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout))

		// Acknowledge the envelope.
		if msg.EnvelopeID != "" {
			ack := map[string]string{"envelope_id": msg.EnvelopeID}
			if err := conn.WriteJSON(ack); err != nil {
				s.logger.Warn("websocket ack error", slog.String("error", err.Error()))
				return
			}
		}

		if msg.Type == "events_api" {
			s.handleEventPayload(ctx, msg.Payload)
		}
	}
}

// handleEventPayload processes an event_callback payload.
func (s *SlackMessenger) handleEventPayload(ctx context.Context, raw json.RawMessage) {
	var payload slackEventPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		s.logger.Warn("failed to unmarshal event payload", slog.String("error", err.Error()))
		return
	}

	evt := payload.Event
	if evt.Type != "message" {
		return
	}

	// Skip bot's own messages, message_changed, message_deleted.
	if evt.SubType == "message_changed" || evt.SubType == "message_deleted" {
		return
	}
	if evt.BotID != "" {
		return
	}
	if s.botUserID != "" && evt.User == s.botUserID {
		return
	}

	// Check if channel is in listen list.
	if !s.isListenChannel(evt.Channel) {
		return
	}

	if s.handler == nil {
		return
	}

	// Check for @bot mention in a thread.
	if s.botUserID != "" && strings.Contains(evt.Text, "<@"+s.botUserID+">") && evt.ThreadTS != "" {
		s.handleBotMention(ctx, evt)
		return
	}

	// Treat as an alert message from Alertmanager bot or similar.
	s.handleAlertMessage(evt)
}

// handleBotMention processes a @bot mention in a thread.
func (s *SlackMessenger) handleBotMention(ctx context.Context, evt slackEvent) {
	// Fetch parent message.
	parentText := s.fetchParentMessage(ctx, evt.Channel, evt.ThreadTS)

	// Strip the bot mention from the command text.
	command := strings.TrimSpace(strings.ReplaceAll(evt.Text, "<@"+s.botUserID+">", ""))

	alert := &domain.Alert{
		Source:     "slack",
		Name:       "thread-mention",
		RawText:    parentText,
		ReceivedAt: time.Now(),
		ReplyTarget: &domain.ReplyTarget{
			Messenger: "slack",
			Channel:   evt.Channel,
			ThreadID:  evt.ThreadTS,
		},
		UserCommand: command,
	}

	s.handler(alert)
}

// handleAlertMessage treats a channel message as an alert.
func (s *SlackMessenger) handleAlertMessage(evt slackEvent) {
	threadID := evt.ThreadTS
	if threadID == "" {
		threadID = evt.TS
	}

	alert := &domain.Alert{
		Source:     "slack",
		Name:       "slack-alert",
		RawText:    evt.Text,
		ReceivedAt: time.Now(),
		ReplyTarget: &domain.ReplyTarget{
			Messenger: "slack",
			Channel:   evt.Channel,
			ThreadID:  threadID,
		},
	}

	s.handler(alert)
}

// fetchParentMessage retrieves the first message in a thread.
func (s *SlackMessenger) fetchParentMessage(ctx context.Context, channel, threadTS string) string {
	url := fmt.Sprintf("%s/conversations.replies?channel=%s&ts=%s&limit=1", s.baseURL, neturl.QueryEscape(channel), neturl.QueryEscape(threadTS))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+s.botToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		OK       bool `json:"ok"`
		Messages []struct {
			Text string `json:"text"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	if result.OK && len(result.Messages) > 0 {
		return result.Messages[0].Text
	}
	return ""
}

// isListenChannel checks whether the channel is in the listen list.
func (s *SlackMessenger) isListenChannel(channel string) bool {
	if len(s.listenChannels) == 0 {
		return true
	}
	for _, ch := range s.listenChannels {
		if ch == channel {
			return true
		}
	}
	return false
}
