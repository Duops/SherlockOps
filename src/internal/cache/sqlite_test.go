package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.db")
}

func TestSetAndGet(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	result := &domain.AnalysisResult{
		AlertFingerprint: "fp-001",
		Text:             "This is the analysis text.",
		ToolsUsed:        []string{"kubectl", "promql"},
	}

	if err := c.Set(ctx, result); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := c.Get(ctx, "fp-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil, expected cached result")
	}
	if got.Text != result.Text {
		t.Errorf("text = %q, want %q", got.Text, result.Text)
	}
	if len(got.ToolsUsed) != 2 || got.ToolsUsed[0] != "kubectl" {
		t.Errorf("tools_used = %v, want [kubectl promql]", got.ToolsUsed)
	}
}

func TestGetMiss(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	got, err := c.Get(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for cache miss, got %+v", got)
	}
}

func TestTTLExpiration(t *testing.T) {
	c, err := New(tempDB(t), 1*time.Millisecond, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	result := &domain.AnalysisResult{
		AlertFingerprint: "fp-ttl",
		Text:             "short-lived entry",
	}
	if err := c.Set(ctx, result); err != nil {
		t.Fatalf("Set: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	got, err := c.Get(ctx, "fp-ttl")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for expired entry")
	}
}

func TestMinLengthGate(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 20)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	short := &domain.AnalysisResult{
		AlertFingerprint: "fp-short",
		Text:             "too short",
	}
	if err := c.Set(ctx, short); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := c.Get(ctx, "fp-short")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil: text below minLength should not be cached")
	}
}

func TestMarkResolved(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	result := &domain.AnalysisResult{
		AlertFingerprint: "fp-resolve",
		Text:             "analysis for resolved test",
	}
	if err := c.Set(ctx, result); err != nil {
		t.Fatalf("Set: %v", err)
	}

	resolvedAt := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.MarkResolved(ctx, "fp-resolve", resolvedAt); err != nil {
		t.Fatalf("MarkResolved: %v", err)
	}

	got, err := c.Get(ctx, "fp-resolve")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected cached result after MarkResolved")
	}
	if got.ResolvedAt == nil {
		t.Fatal("expected ResolvedAt to be set")
	}
	if !got.ResolvedAt.Equal(resolvedAt) {
		t.Errorf("ResolvedAt = %v, want %v", got.ResolvedAt, resolvedAt)
	}
}

func TestNewInvalidPath(t *testing.T) {
	_, err := New(filepath.Join(os.DevNull, "impossible", "path.db"), time.Hour, 5)
	if err == nil {
		t.Fatal("expected error for invalid db path")
	}
}

func TestListEmpty(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	results, total, err := c.List(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
	if len(results) != 0 {
		t.Errorf("results = %d, want 0", len(results))
	}
}

func TestListWithPagination(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()

	// Insert 5 entries.
	for i := 0; i < 5; i++ {
		result := &domain.AnalysisResult{
			AlertFingerprint: fmt.Sprintf("fp-list-%d", i),
			Text:             fmt.Sprintf("Analysis text for list test entry %d", i),
			ToolsUsed:        []string{"tool-x"},
		}
		if err := c.Set(ctx, result); err != nil {
			t.Fatalf("Set(%d): %v", i, err)
		}
	}

	// First page: limit=2, offset=0.
	results, total, err := c.List(ctx, 2, 0)
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(results) != 2 {
		t.Errorf("page 1 results = %d, want 2", len(results))
	}

	// Second page: limit=2, offset=2.
	results2, total2, err := c.List(ctx, 2, 2)
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if total2 != 5 {
		t.Errorf("total = %d, want 5", total2)
	}
	if len(results2) != 2 {
		t.Errorf("page 2 results = %d, want 2", len(results2))
	}

	// Third page: limit=2, offset=4.
	results3, _, err := c.List(ctx, 2, 4)
	if err != nil {
		t.Fatalf("List page 3: %v", err)
	}
	if len(results3) != 1 {
		t.Errorf("page 3 results = %d, want 1", len(results3))
	}
}

func TestListDefaultLimitAndNegativeOffset(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	result := &domain.AnalysisResult{
		AlertFingerprint: "fp-default",
		Text:             "Analysis text for default limit test",
	}
	if err := c.Set(ctx, result); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// limit=0 should default to 50, offset=-1 should default to 0.
	results, total, err := c.List(ctx, 0, -1)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(results) != 1 {
		t.Errorf("results = %d, want 1", len(results))
	}
}

func TestListReturnsResolvedAt(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	result := &domain.AnalysisResult{
		AlertFingerprint: "fp-list-resolved",
		Text:             "Analysis text for resolved list test",
		ToolsUsed:        []string{"kubectl"},
	}
	if err := c.Set(ctx, result); err != nil {
		t.Fatalf("Set: %v", err)
	}

	resolvedAt := time.Date(2025, 7, 1, 10, 0, 0, 0, time.UTC)
	if err := c.MarkResolved(ctx, "fp-list-resolved", resolvedAt); err != nil {
		t.Fatalf("MarkResolved: %v", err)
	}

	results, _, err := c.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ResolvedAt == nil {
		t.Fatal("expected ResolvedAt to be set in List result")
	}
	if !results[0].ResolvedAt.Equal(resolvedAt) {
		t.Errorf("ResolvedAt = %v, want %v", results[0].ResolvedAt, resolvedAt)
	}
	if len(results[0].ToolsUsed) != 1 || results[0].ToolsUsed[0] != "kubectl" {
		t.Errorf("ToolsUsed = %v, want [kubectl]", results[0].ToolsUsed)
	}
}

func TestStatsEmpty(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	stats, err := c.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TotalCount != 0 {
		t.Errorf("TotalCount = %d, want 0", stats.TotalCount)
	}
	if stats.ResolvedCount != 0 {
		t.Errorf("ResolvedCount = %d, want 0", stats.ResolvedCount)
	}
	if stats.AvgTextLength != 0 {
		t.Errorf("AvgTextLength = %f, want 0", stats.AvgTextLength)
	}
}

func TestStatsWithData(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()

	// Insert 3 entries, mark 1 as resolved.
	entries := []struct {
		fp   string
		text string
	}{
		{"fp-stats-1", "Analysis text one here"},
		{"fp-stats-2", "Analysis text two here with more"},
		{"fp-stats-3", "Analysis three"},
	}
	for _, e := range entries {
		result := &domain.AnalysisResult{
			AlertFingerprint: e.fp,
			Text:             e.text,
		}
		if err := c.Set(ctx, result); err != nil {
			t.Fatalf("Set(%s): %v", e.fp, err)
		}
	}

	if err := c.MarkResolved(ctx, "fp-stats-2", time.Now()); err != nil {
		t.Fatalf("MarkResolved: %v", err)
	}

	stats, err := c.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TotalCount != 3 {
		t.Errorf("TotalCount = %d, want 3", stats.TotalCount)
	}
	if stats.ResolvedCount != 1 {
		t.Errorf("ResolvedCount = %d, want 1", stats.ResolvedCount)
	}
	if stats.AvgTextLength <= 0 {
		t.Errorf("AvgTextLength = %f, want > 0", stats.AvgTextLength)
	}
}

func TestCloseAndOperationsOnClosedCache(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Close should succeed.
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Operations on a closed cache should return errors.
	ctx := context.Background()

	_, err = c.Get(ctx, "fp-closed")
	if err == nil {
		t.Error("expected error on Get after Close")
	}

	err = c.Set(ctx, &domain.AnalysisResult{
		AlertFingerprint: "fp-closed",
		Text:             "should fail because cache is closed",
	})
	if err == nil {
		t.Error("expected error on Set after Close")
	}

	err = c.MarkResolved(ctx, "fp-closed", time.Now())
	if err == nil {
		t.Error("expected error on MarkResolved after Close")
	}

	_, _, err = c.List(ctx, 10, 0)
	if err == nil {
		t.Error("expected error on List after Close")
	}

	_, err = c.Stats(ctx)
	if err == nil {
		t.Error("expected error on Stats after Close")
	}
}

func TestDoubleClose(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Second close: should not panic, may or may not return error.
	_ = c.Close()
}
