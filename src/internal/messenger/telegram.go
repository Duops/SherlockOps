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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

// convertSlackToTelegram converts Slack-style formatting (*bold*, _italic_)
// to Telegram format (HTML or MarkdownV2).
// LLM system prompt produces Slack-compatible markup; this adapts it for Telegram.
func convertSlackToTelegram(text, parseMode string) string {
	if parseMode == "HTML" {
		// *text* → <b>text</b>, _text_ → <i>text</i>
		// Process bold: *...*  (but not ** which shouldn't appear per prompt rules)
		result := &strings.Builder{}
		i := 0
		for i < len(text) {
			if text[i] == '*' {
				end := strings.Index(text[i+1:], "*")
				if end > 0 {
					result.WriteString("<b>")
					result.WriteString(text[i+1 : i+1+end])
					result.WriteString("</b>")
					i = i + 1 + end + 1
					continue
				}
			}
			if text[i] == '_' {
				end := strings.Index(text[i+1:], "_")
				if end > 0 {
					result.WriteString("<i>")
					result.WriteString(text[i+1 : i+1+end])
					result.WriteString("</i>")
					i = i + 1 + end + 1
					continue
				}
			}
			result.WriteByte(text[i])
			i++
		}
		return result.String()
	}
	// MarkdownV2: *text* → **text**
	result := &strings.Builder{}
	i := 0
	for i < len(text) {
		if text[i] == '*' {
			end := strings.Index(text[i+1:], "*")
			if end > 0 {
				result.WriteString("**")
				result.WriteString(text[i+1 : i+1+end])
				result.WriteString("**")
				i = i + 1 + end + 1
				continue
			}
		}
		result.WriteByte(text[i])
		i++
	}
	return result.String()
}

// TelegramMessenger implements domain.Messenger using raw Telegram Bot API.
type TelegramMessenger struct {
	botToken    string
	defaultChat int64
	listenChats []int64
	parseMode   string
	client      *http.Client
	handler     func(alert *domain.Alert)
	logger      *slog.Logger
	cancel      context.CancelFunc
	offset      int64
	baseURL     string // overridable for testing
	mu          sync.Mutex
}

// NewTelegram creates a new TelegramMessenger.
func NewTelegram(botToken string, defaultChat int64, listenChats []int64, parseMode string, logger *slog.Logger) *TelegramMessenger {
	if parseMode == "" {
		parseMode = "HTML"
	}
	return &TelegramMessenger{
		botToken:    botToken,
		defaultChat: defaultChat,
		listenChats: listenChats,
		parseMode:   parseMode,
		client:      &http.Client{Timeout: 60 * time.Second},
		logger:      logger,
		baseURL:     fmt.Sprintf("https://api.telegram.org/bot%s", botToken),
	}
}

func (t *TelegramMessenger) Name() string {
	return "telegram"
}

// Start begins long polling for Telegram updates.
func (t *TelegramMessenger) Start(ctx context.Context, handler func(alert *domain.Alert)) error {
	t.handler = handler

	ctx, cancel := context.WithCancel(ctx)
	t.mu.Lock()
	t.cancel = cancel
	t.mu.Unlock()

	go t.pollLoop(ctx)
	return nil
}

// Stop gracefully shuts down the messenger.
func (t *TelegramMessenger) Stop(_ context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	return nil
}

// SendAlert posts a rich alert to a Telegram chat and returns a MessageRef (Phase 1).
func (t *TelegramMessenger) SendAlert(ctx context.Context, alert *domain.Alert) (*domain.MessageRef, error) {
	chatID, _ := t.resolveTarget(alert)
	text := formatTelegramAlert(alert, t.parseMode)
	msgID, err := t.sendMessage(ctx, chatID, 0, text)
	if err != nil {
		return nil, err
	}
	return &domain.MessageRef{
		Messenger: "telegram",
		Channel:   fmt.Sprintf("%d", chatID),
		MessageID: fmt.Sprintf("%d", msgID),
		Alert:     alert,
	}, nil
}

// SendAnalysisReply edits the original alert message to append analysis (Phase 2).
func (t *TelegramMessenger) SendAnalysisReply(ctx context.Context, ref *domain.MessageRef, result *domain.AnalysisResult) error {
	chatID, _ := strconv.ParseInt(ref.Channel, 10, 64)
	msgID, _ := strconv.ParseInt(ref.MessageID, 10, 64)

	alertText := formatTelegramAlert(ref.Alert, t.parseMode)
	analysisText := formatTelegramAnalysisRich(result, t.parseMode)
	newText := alertText + "\n\n" + analysisText
	return t.editMessage(ctx, chatID, msgID, newText)
}

// SendAnalysis posts an analysis result to Telegram.
func (t *TelegramMessenger) SendAnalysis(ctx context.Context, alert *domain.Alert, result *domain.AnalysisResult) error {
	chatID, replyToMsgID := t.resolveTarget(alert)
	text := formatTelegramAnalysis(alert, result, t.parseMode)
	_, err := t.sendMessage(ctx, chatID, replyToMsgID, text)
	return err
}

// SendError posts an error message to Telegram.
// Only a generic message is sent to the user; the full error is logged server-side
// to avoid leaking internal details (API keys, URLs, stack traces).
func (t *TelegramMessenger) SendError(ctx context.Context, alert *domain.Alert, err error) error {
	t.logger.Error("alert analysis failed", "alert", alert.Name, "error", err)

	chatID, replyToMsgID := t.resolveTarget(alert)

	var text string
	if t.parseMode == "HTML" {
		text = fmt.Sprintf("⚠️ <b>Error analyzing alert</b> <code>%s</code>\nAn internal error occurred. Please check the server logs.", alert.Name)
	} else {
		text = fmt.Sprintf("⚠️ *Error analyzing alert* `%s`\nAn internal error occurred. Please check the server logs.", alert.Name)
	}

	_, sendErr := t.sendMessage(ctx, chatID, replyToMsgID, text)
	return sendErr
}

func (t *TelegramMessenger) resolveTarget(alert *domain.Alert) (chatID int64, replyToMsgID int64) {
	chatID = t.defaultChat
	// Check ChannelOverrides from X-Channel-Telegram header.
	if ch, ok := alert.ChannelOverrides["telegram"]; ok && ch != "" {
		if parsed, err := strconv.ParseInt(ch, 10, 64); err == nil {
			chatID = parsed
		}
	}
	if alert.ReplyTarget != nil && alert.ReplyTarget.Messenger == "telegram" {
		if parsed, err := strconv.ParseInt(alert.ReplyTarget.Channel, 10, 64); err == nil {
			chatID = parsed
		}
		if alert.ReplyTarget.ThreadID != "" {
			if parsed, err := strconv.ParseInt(alert.ReplyTarget.ThreadID, 10, 64); err == nil {
				replyToMsgID = parsed
			}
		}
	}
	return chatID, replyToMsgID
}

func formatTelegramAnalysis(alert *domain.Alert, result *domain.AnalysisResult, parseMode string) string {
	var sb strings.Builder

	converted := convertSlackToTelegram(result.Text, parseMode)

	if parseMode == "HTML" {
		sb.WriteString(fmt.Sprintf("<b>Alert Analysis: %s</b>\n", alert.Name))
		if alert.Severity != "" {
			sb.WriteString(fmt.Sprintf("<i>Severity: %s | Status: %s</i>\n", alert.Severity, alert.Status))
		}
		sb.WriteString("\n")
		sb.WriteString(converted)
		if len(result.ToolsUsed) > 0 {
			sb.WriteString(fmt.Sprintf("\n\n<i>Tools used: %s</i>", strings.Join(result.ToolsUsed, ", ")))
		}
	} else {
		sb.WriteString(fmt.Sprintf("*Alert Analysis: %s*\n", alert.Name))
		if alert.Severity != "" {
			sb.WriteString(fmt.Sprintf("_Severity: %s | Status: %s_\n", alert.Severity, alert.Status))
		}
		sb.WriteString("\n")
		sb.WriteString(converted)
		if len(result.ToolsUsed) > 0 {
			sb.WriteString(fmt.Sprintf("\n\n_Tools used: %s_", strings.Join(result.ToolsUsed, ", ")))
		}
	}

	return sb.String()
}

// formatTelegramAlert formats a rich alert message for Telegram (Phase 1).
func formatTelegramAlert(alert *domain.Alert, parseMode string) string {
	sev := alertSeverityEmoji(alert.Severity, alert.Status)
	status := "FIRING"
	if alert.Status == domain.StatusResolved {
		status = "RESOLVED"
	}

	targetType, targetName := extractTarget(alert)

	var sb strings.Builder

	if parseMode == "HTML" {
		sb.WriteString(fmt.Sprintf("%s <b>[%s] %s</b>", sev, status, alert.Name))
		if alert.Severity != "" {
			sb.WriteString(fmt.Sprintf("\nLevel: <code>%s</code>", alert.Severity))
		}
		if env := alert.Labels["cluster"]; env != "" {
			sb.WriteString(fmt.Sprintf(" | Env: <code>%s</code>", env))
		}
		if targetName != "" {
			sb.WriteString(fmt.Sprintf("\n\nTarget: <code>%s: %s</code>", targetType, targetName))
		}
		if summary := alert.Annotations["summary"]; summary != "" {
			sb.WriteString(fmt.Sprintf("\nMessage: %s", summary))
		}
		ctxText := formatLabelsContext(alert.Labels, targetType)
		if ctxText != "" {
			sb.WriteString(fmt.Sprintf("\n\n<i>%s</i>", ctxText))
		}
	} else {
		sb.WriteString(fmt.Sprintf("%s *[%s] %s*", sev, status, alert.Name))
		if alert.Severity != "" {
			sb.WriteString(fmt.Sprintf("\nLevel: `%s`", alert.Severity))
		}
		if env := alert.Labels["cluster"]; env != "" {
			sb.WriteString(fmt.Sprintf(" | Env: `%s`", env))
		}
		if targetName != "" {
			sb.WriteString(fmt.Sprintf("\n\nTarget: `%s: %s`", targetType, targetName))
		}
		if summary := alert.Annotations["summary"]; summary != "" {
			sb.WriteString(fmt.Sprintf("\nMessage: %s", summary))
		}
		ctxText := formatLabelsContext(alert.Labels, targetType)
		if ctxText != "" {
			sb.WriteString(fmt.Sprintf("\n\n_%s_", ctxText))
		}
	}

	return sb.String()
}

// formatTelegramAnalysisRich wraps LLM analysis with investigation header and tools footer for Telegram.
func formatTelegramAnalysisRich(result *domain.AnalysisResult, parseMode string) string {
	var sb strings.Builder

	separator := "\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501"

	converted := convertSlackToTelegram(result.Text, parseMode)

	if parseMode == "HTML" {
		sb.WriteString(separator)
		sb.WriteString("\n\U0001F50D <b>SherlockOps Investigation</b>\n")
		if badge := formatCacheBadge(result); badge != "" {
			sb.WriteString(badge)
		}
		sb.WriteString("\n")
		sb.WriteString(converted)
		trace := formatToolsTraceFromResult(result)
		if trace != "" {
			sb.WriteString(fmt.Sprintf("\n\n<i>\U0001F6E0\uFE0F Tools: %s</i>", trace))
		}
	} else {
		sb.WriteString(separator)
		sb.WriteString("\n\U0001F50D *SherlockOps Investigation*\n")
		if badge := formatCacheBadge(result); badge != "" {
			sb.WriteString(badge)
		}
		sb.WriteString("\n")
		sb.WriteString(converted)
		trace := formatToolsTraceFromResult(result)
		if trace != "" {
			sb.WriteString(fmt.Sprintf("\n\n_\U0001F6E0\uFE0F Tools: %s_", trace))
		}
	}

	return sb.String()
}

// sendMessage sends a message via Telegram Bot API with retry on 429.
// It returns the message_id from the Telegram API response.
func (t *TelegramMessenger) sendMessage(ctx context.Context, chatID, replyToMsgID int64, text string) (int64, error) {
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": t.parseMode,
	}
	if replyToMsgID != 0 {
		payload["reply_to_message_id"] = replyToMsgID
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal message: %w", err)
	}

	respBody, err := t.doWithRetry(ctx, t.baseURL+"/sendMessage", body)
	if err != nil {
		return 0, err
	}

	var result struct {
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return 0, fmt.Errorf("decode message_id from response: %w", err)
	}
	return result.Result.MessageID, nil
}

// editMessage edits an existing message via Telegram Bot API.
func (t *TelegramMessenger) editMessage(ctx context.Context, chatID, messageID int64, text string) error {
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
		"parse_mode": t.parseMode,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal edit message: %w", err)
	}

	_, err = t.doWithRetry(ctx, t.baseURL+"/editMessageText", body)
	return err
}

// doWithRetry sends a POST request with retry on 429.
// It returns the raw response body on success.
func (t *TelegramMessenger) doWithRetry(ctx context.Context, url string, body []byte) ([]byte, error) {
	const maxRetries = 5
	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := t.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second

			// Try to extract retry_after from response.
			var retryResp struct {
				Parameters struct {
					RetryAfter int `json:"retry_after"`
				} `json:"parameters"`
			}
			if json.Unmarshal(respBody, &retryResp) == nil && retryResp.Parameters.RetryAfter > 0 {
				backoff = time.Duration(retryResp.Parameters.RetryAfter) * time.Second
			}

			t.logger.Warn("rate limited by Telegram, retrying",
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
			return nil, fmt.Errorf("telegram API error: status=%d body=%s", resp.StatusCode, string(respBody))
		}

		var tgResp struct {
			OK          bool   `json:"ok"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(respBody, &tgResp); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		if !tgResp.OK {
			return nil, fmt.Errorf("telegram API error: %s", tgResp.Description)
		}
		return respBody, nil
	}
	return nil, fmt.Errorf("telegram API: max retries exceeded")
}

// telegramUpdate represents a Telegram update from getUpdates.
type telegramUpdate struct {
	UpdateID int64           `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

// telegramMessage represents a Telegram message.
type telegramMessage struct {
	MessageID      int64              `json:"message_id"`
	Chat           telegramChat       `json:"chat"`
	Text           string             `json:"text"`
	ReplyToMessage *telegramMessage   `json:"reply_to_message"`
	From           *telegramUser      `json:"from"`
	Entities       []telegramEntity   `json:"entities"`
}

// telegramChat represents a Telegram chat.
type telegramChat struct {
	ID int64 `json:"id"`
}

// telegramUser represents a Telegram user.
type telegramUser struct {
	ID    int64 `json:"id"`
	IsBot bool  `json:"is_bot"`
}

// telegramEntity represents a message entity (commands, mentions, etc.).
type telegramEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

// pollLoop runs the Telegram long polling loop.
func (t *TelegramMessenger) pollLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := t.getUpdates(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			t.logger.Error("failed to get updates", slog.String("error", err.Error()))
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		for _, update := range updates {
			t.offset = update.UpdateID + 1
			t.handleUpdate(update)
		}
	}
}

// getUpdates fetches updates from the Telegram Bot API.
func (t *TelegramMessenger) getUpdates(ctx context.Context) ([]telegramUpdate, error) {
	url := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=30", t.baseURL, t.offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool             `json:"ok"`
		Result []telegramUpdate `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("getUpdates failed")
	}
	return result.Result, nil
}

// handleUpdate processes a single Telegram update.
func (t *TelegramMessenger) handleUpdate(update telegramUpdate) {
	if update.Message == nil {
		return
	}

	msg := update.Message

	// Skip messages from bots.
	if msg.From != nil && msg.From.IsBot {
		return
	}

	// Check if chat is in listen list.
	if !t.isListenChat(msg.Chat.ID) {
		return
	}

	if t.handler == nil {
		return
	}

	// Check for bot commands.
	if command, args := t.extractCommand(msg); command != "" {
		t.handleCommand(msg, command, args)
		return
	}

	// Handle replies to alert messages.
	if msg.ReplyToMessage != nil {
		t.handleReply(msg)
		return
	}
}

// extractCommand extracts a bot command and its arguments from a message.
func (t *TelegramMessenger) extractCommand(msg *telegramMessage) (string, string) {
	for _, entity := range msg.Entities {
		if entity.Type == "bot_command" {
			runes := []rune(msg.Text)
			cmd := string(runes[entity.Offset : entity.Offset+entity.Length])
			// Strip @botname suffix if present.
			if idx := strings.Index(cmd, "@"); idx > 0 {
				cmd = cmd[:idx]
			}
			args := ""
			if entity.Offset+entity.Length < len(runes) {
				args = strings.TrimSpace(string(runes[entity.Offset+entity.Length:]))
			}
			return cmd, args
		}
	}
	return "", ""
}

// handleCommand processes a bot command.
func (t *TelegramMessenger) handleCommand(msg *telegramMessage, command, args string) {
	var rawText string
	if msg.ReplyToMessage != nil {
		rawText = msg.ReplyToMessage.Text
	}

	threadID := ""
	if msg.ReplyToMessage != nil {
		threadID = strconv.FormatInt(msg.ReplyToMessage.MessageID, 10)
	}

	userCommand := command
	if args != "" {
		userCommand = command + " " + args
	}

	alert := &domain.Alert{
		Source:     "telegram",
		Name:       "telegram-command",
		RawText:    rawText,
		ReceivedAt: time.Now(),
		ReplyTarget: &domain.ReplyTarget{
			Messenger: "telegram",
			Channel:   strconv.FormatInt(msg.Chat.ID, 10),
			ThreadID:  threadID,
		},
		UserCommand: userCommand,
	}

	t.handler(alert)
}

// handleReply processes a reply to an existing message.
func (t *TelegramMessenger) handleReply(msg *telegramMessage) {
	alert := &domain.Alert{
		Source:     "telegram",
		Name:       "telegram-reply",
		RawText:    msg.ReplyToMessage.Text,
		ReceivedAt: time.Now(),
		ReplyTarget: &domain.ReplyTarget{
			Messenger: "telegram",
			Channel:   strconv.FormatInt(msg.Chat.ID, 10),
			ThreadID:  strconv.FormatInt(msg.ReplyToMessage.MessageID, 10),
		},
		UserCommand: msg.Text,
	}

	t.handler(alert)
}

// isListenChat checks whether the chat ID is in the listen list.
func (t *TelegramMessenger) isListenChat(chatID int64) bool {
	if len(t.listenChats) == 0 {
		return true
	}
	for _, id := range t.listenChats {
		if id == chatID {
			return true
		}
	}
	return false
}
