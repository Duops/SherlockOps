package review

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

type fakeLLM struct {
	mu    sync.Mutex
	reqs  []*domain.ChatRequest
	reply string
	delay time.Duration
}

func (f *fakeLLM) Chat(_ context.Context, req *domain.ChatRequest) (*domain.ChatResponse, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	time.Sleep(f.delay)
	return &domain.ChatResponse{Content: f.reply, Done: true, InputTokens: 100, OutputTokens: 40}, nil
}

type fakeStats struct{ stats *domain.AlertStats }

func (f *fakeStats) AlertStats(_ context.Context, since time.Time, env string) (*domain.AlertStats, error) {
	s := *f.stats
	s.Environment = env
	s.Since = since
	return &s, nil
}

type fakeStore struct {
	mu      sync.Mutex
	reviews []*domain.AlertReview
}

func (f *fakeStore) SaveReview(_ context.Context, r *domain.AlertReview) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.ID = int64(len(f.reviews) + 1)
	f.reviews = append(f.reviews, r)
	return nil
}

func (f *fakeStore) LatestReview(_ context.Context, env string) (*domain.AlertReview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.reviews) - 1; i >= 0; i-- {
		if f.reviews[i].Environment == env {
			return f.reviews[i], nil
		}
	}
	return nil, nil
}

func sampleStats() *domain.AlertStats {
	return &domain.AlertStats{
		Days: 7, Total: 100, Firing: 80, Resolved: 20, PerDay: 14, UniqueAlerts: 2,
		Top3Share: 100,
		TopAlerts: []domain.AlertCount{
			{Name: "TargetDown", Environments: []string{"prod"}, Count: 70, Firing: 60, Resolved: 10, Share: 70},
			{Name: "CPUThrottlingHigh", Environments: []string{"dev", "prod"}, Count: 30, Firing: 20, Resolved: 10, Share: 30},
		},
		ByEnvironment: []domain.EnvCount{
			{Environment: "prod", Count: 85, UniqueAlerts: 2, Share: 85, TopAlert: "TargetDown"},
			{Environment: "dev", Count: 15, UniqueAlerts: 1, Share: 15, TopAlert: "CPUThrottlingHigh"},
		},
	}
}

func newReviewer(llm *fakeLLM, store *fakeStore, lang string) *Reviewer {
	return New(llm, &fakeStats{stats: sampleStats()}, store, "claude-sonnet-4-6", lang, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestRun_BuildsPromptAndStoresReview(t *testing.T) {
	llm := &fakeLLM{reply: "  ## Summary\nRaise TargetDown for to 10m.  "}
	store := &fakeStore{}
	r := newReviewer(llm, store, "ru")

	rev, err := r.Run(context.Background(), "", 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rev.Text != "## Summary\nRaise TargetDown for to 10m." || rev.Model != "claude-sonnet-4-6" {
		t.Errorf("unexpected review: %+v", rev)
	}
	if rev.InputTokens != 100 || rev.OutputTokens != 40 || rev.CostUSD <= 0 {
		t.Errorf("tokens/cost not recorded: %+v", rev)
	}
	if len(store.reviews) != 1 || store.reviews[0].ID != 1 {
		t.Errorf("review not stored: %+v", store.reviews)
	}

	req := llm.reqs[0]
	if !strings.Contains(req.SystemPrompt, "SRE") || !strings.Contains(req.SystemPrompt, "Рекомендации") {
		t.Errorf("unexpected system prompt: %s", req.SystemPrompt)
	}
	user := req.Messages[0].Content
	for _, want := range []string{"TargetDown | prod | 70 | 60 | 10 | 70.0%", "CPUThrottlingHigh | dev,prod", "prod | 85 | 2 | 85.0% | TargetDown", "Период: 7 дней"} {
		if !strings.Contains(user, want) {
			t.Errorf("prompt missing %q:\n%s", want, user)
		}
	}
	if len(req.Tools) != 0 {
		t.Error("review must not offer tools")
	}
}

func TestRun_EnglishPromptAndEnvFilter(t *testing.T) {
	llm := &fakeLLM{reply: "ok"}
	store := &fakeStore{}
	r := newReviewer(llm, store, "en")
	rev, err := r.Run(context.Background(), "prod", 24*time.Hour)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rev.Environment != "prod" {
		t.Errorf("environment = %q", rev.Environment)
	}
	if got := llm.reqs[0].Messages[0].Content; !strings.Contains(got, "environment: prod") || !strings.Contains(got, "Period: 7 days") {
		t.Errorf("unexpected english prompt: %s", got)
	}
}

func TestRun_NoDataIsError(t *testing.T) {
	r := New(&fakeLLM{reply: "x"}, &fakeStats{stats: &domain.AlertStats{}}, &fakeStore{}, "m", "en", nil)
	if _, err := r.Run(context.Background(), "", time.Hour); err == nil {
		t.Fatal("expected error when there are no notifications")
	}
}

func TestStartReview_OnlyOneAtATime(t *testing.T) {
	llm := &fakeLLM{reply: "ok", delay: 150 * time.Millisecond}
	store := &fakeStore{}
	r := newReviewer(llm, store, "en")

	if err := r.StartReview("", time.Hour); err != nil {
		t.Fatalf("StartReview: %v", err)
	}
	if err := r.StartReview("", time.Hour); !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("second StartReview = %v, want ErrAlreadyRunning", err)
	}
	if !r.Running() {
		t.Error("Running should be true while generating")
	}
	deadline := time.Now().Add(2 * time.Second)
	for r.Running() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got, _ := r.LatestReview(context.Background(), ""); got == nil || got.Text != "ok" {
		t.Errorf("review not stored after background run: %+v", got)
	}
}

func TestRunLoop_SkipsWhenRecentReviewExists(t *testing.T) {
	llm := &fakeLLM{reply: "ok"}
	store := &fakeStore{reviews: []*domain.AlertReview{{Environment: "", Text: "recent", CreatedAt: time.Now()}}}
	r := newReviewer(llm, store, "en")
	ctx, cancel := context.WithCancel(context.Background())
	go r.RunLoop(ctx, 7*24*time.Hour)
	time.Sleep(50 * time.Millisecond)
	cancel()
	if len(llm.reqs) != 0 {
		t.Errorf("expected no LLM calls when a recent review exists, got %d", len(llm.reqs))
	}

	stale := &fakeStore{reviews: []*domain.AlertReview{{Environment: "", Text: "old", CreatedAt: time.Now().Add(-8 * 24 * time.Hour)}}}
	llm2 := &fakeLLM{reply: "fresh"}
	r2 := newReviewer(llm2, stale, "en")
	ctx2, cancel2 := context.WithCancel(context.Background())
	go r2.RunLoop(ctx2, 7*24*time.Hour)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := stale.LatestReview(context.Background(), ""); got != nil && got.Text == "fresh" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel2()
	if got, _ := stale.LatestReview(context.Background(), ""); got == nil || got.Text != "fresh" {
		t.Errorf("expected a fresh review when the latest is stale, got %+v", got)
	}
}
