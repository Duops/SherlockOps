package pipeline

import (
	"context"
	"strings"

	"github.com/Duops/SherlockOps/internal/domain"
)

// analyzeKeywords are case-insensitive substrings that mark the user's reply
// as an explicit "please analyze this alert" command. Both Russian forms
// ("проанализируй", "переанализируй", "анализируй") match via "анализ".
var analyzeKeywords = []string{
	"analyz", // analyze, analyse, analyzing, analyzed
	"анализ", // анализ, анализируй, проанализируй, переанализируй
}

// IsAnalyzeCommand reports whether the user's mention text contains an explicit
// analyze command. Case-insensitive substring match against analyzeKeywords.
func IsAnalyzeCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	c := strings.ToLower(strings.TrimSpace(cmd))
	if c == "" {
		return false
	}
	for _, k := range analyzeKeywords {
		if strings.Contains(c, k) {
			return true
		}
	}
	return false
}

// ResolvePendingMention checks whether the alert is a bot mention that should
// trigger analysis of a previously stored manual-mode alert. If so, it loads
// the original alert from the pending store, copies over routing fields
// (ReplyTarget, RequestID), marks it as a forced reanalysis, and returns the
// recovered alert. When no pending entry matches, the original alert is
// returned unchanged so the caller can fall back to the existing flow.
//
// store may be nil — in that case the function is a no-op pass-through.
func ResolvePendingMention(ctx context.Context, store domain.PendingStore, alert *domain.Alert) *domain.Alert {
	if store == nil || alert == nil {
		return alert
	}
	if alert.ReplyTarget == nil || alert.ReplyTarget.ThreadID == "" {
		return alert
	}
	if !IsAnalyzeCommand(alert.UserCommand) {
		return alert
	}
	pending, err := store.GetPending(ctx, alert.ReplyTarget.Messenger, alert.ReplyTarget.Channel, alert.ReplyTarget.ThreadID)
	if err != nil || pending == nil {
		return alert
	}
	pending.ReplyTarget = alert.ReplyTarget
	pending.RequestID = alert.RequestID
	pending.UserCommand = "reanalyze"
	return pending
}
