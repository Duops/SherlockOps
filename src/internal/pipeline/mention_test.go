package pipeline

import (
	"context"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

func TestIsAnalyzeCommand(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"hi there", false},
		{"please analyze this", true},
		{"@bot analyze", true},
		{"@bot, analyze", true},
		{"проанализируй пожалуйста", true},
		{"АНАЛИЗ", true},
		{"переанализируй", true}, // re-analyze is still an analyze command
		{"reanalyze", true},
		{"@bot status", false},
		{"что тут происходит", false},
	}
	for _, tc := range cases {
		if got := IsAnalyzeCommand(tc.in); got != tc.want {
			t.Errorf("IsAnalyzeCommand(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestResolvePendingMention_NilStore(t *testing.T) {
	alert := &domain.Alert{Name: "x"}
	got := ResolvePendingMention(context.Background(), nil, alert)
	if got != alert {
		t.Errorf("nil store should pass through")
	}
}

func TestResolvePendingMention_NotAMention(t *testing.T) {
	store := newMockPendingStore()
	alert := &domain.Alert{Name: "x"} // no ReplyTarget
	if got := ResolvePendingMention(context.Background(), store, alert); got != alert {
		t.Errorf("non-mention should pass through")
	}
}

func TestResolvePendingMention_MentionWithoutAnalyzeCommand(t *testing.T) {
	store := newMockPendingStore()
	alert := &domain.Alert{
		ReplyTarget: &domain.ReplyTarget{Messenger: "slack", Channel: "C1", ThreadID: "T1"},
		UserCommand: "hello bot",
	}
	if got := ResolvePendingMention(context.Background(), store, alert); got != alert {
		t.Errorf("non-analyze command should pass through")
	}
}

func TestResolvePendingMention_NoPendingEntry(t *testing.T) {
	store := newMockPendingStore()
	alert := &domain.Alert{
		ReplyTarget: &domain.ReplyTarget{Messenger: "slack", Channel: "C1", ThreadID: "T1"},
		UserCommand: "@bot analyze",
	}
	if got := ResolvePendingMention(context.Background(), store, alert); got != alert {
		t.Errorf("missing pending entry should pass through original alert")
	}
}

func TestResolvePendingMention_Swaps(t *testing.T) {
	store := newMockPendingStore()
	original := &domain.Alert{
		Name:        "HighCPU",
		Fingerprint: "fp-original",
		Labels:      map[string]string{"namespace": "prod"},
	}
	ref := &domain.MessageRef{Messenger: "telegram", Channel: "-100", MessageID: "42"}
	if err := store.SavePending(context.Background(), ref, original); err != nil {
		t.Fatalf("SavePending: %v", err)
	}

	mention := &domain.Alert{
		Name:        "thread-mention",
		ReplyTarget: &domain.ReplyTarget{Messenger: "telegram", Channel: "-100", ThreadID: "42"},
		UserCommand: "@bot проанализируй",
		RequestID:   "req-123",
	}

	got := ResolvePendingMention(context.Background(), store, mention)
	if got == mention {
		t.Fatal("expected swap to original alert")
	}
	if got.Fingerprint != "fp-original" || got.Name != "HighCPU" {
		t.Errorf("wrong alert returned: %+v", got)
	}
	if got.ReplyTarget == nil || got.ReplyTarget.ThreadID != "42" {
		t.Errorf("ReplyTarget not preserved on swapped alert")
	}
	if got.RequestID != "req-123" {
		t.Errorf("RequestID not preserved")
	}
	if got.UserCommand != "reanalyze" {
		t.Errorf("UserCommand should be set to reanalyze, got %q", got.UserCommand)
	}
}
