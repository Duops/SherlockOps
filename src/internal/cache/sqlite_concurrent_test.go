package cache

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func TestConcurrentSetAndGet(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	const goroutines = 20
	var wg sync.WaitGroup

	// Concurrently write different fingerprints.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result := &domain.AnalysisResult{
				AlertFingerprint: fmt.Sprintf("fp-concurrent-%d", idx),
				Text:             fmt.Sprintf("Analysis text for concurrent test %d", idx),
				ToolsUsed:        []string{"tool-a", "tool-b"},
			}
			if err := c.Set(ctx, result); err != nil {
				t.Errorf("Set(%d): %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	// Verify all entries are readable.
	for i := 0; i < goroutines; i++ {
		fp := fmt.Sprintf("fp-concurrent-%d", i)
		got, err := c.Get(ctx, fp)
		if err != nil {
			t.Errorf("Get(%s): %v", fp, err)
			continue
		}
		if got == nil {
			t.Errorf("Get(%s): expected cached result, got nil", fp)
			continue
		}
		expectedText := fmt.Sprintf("Analysis text for concurrent test %d", i)
		if got.Text != expectedText {
			t.Errorf("Get(%s): text = %q, want %q", fp, got.Text, expectedText)
		}
	}
}

func TestConcurrentSameFingerprint(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	const goroutines = 10
	var wg sync.WaitGroup

	// Concurrently write to the same fingerprint.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result := &domain.AnalysisResult{
				AlertFingerprint: "fp-same",
				Text:             fmt.Sprintf("Analysis version %d with enough text", idx),
			}
			if err := c.Set(ctx, result); err != nil {
				t.Errorf("Set(%d): %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	// Should have one entry (last write wins).
	got, err := c.Get(ctx, "fp-same")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected cached result for fp-same")
	}
}

func TestConcurrentGetAndMarkResolved(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()

	// Seed data.
	for i := 0; i < 10; i++ {
		result := &domain.AnalysisResult{
			AlertFingerprint: fmt.Sprintf("fp-resolve-%d", i),
			Text:             fmt.Sprintf("Analysis text for resolve test %d", i),
		}
		if err := c.Set(ctx, result); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	var wg sync.WaitGroup

	// Concurrent reads and resolves.
	for i := 0; i < 10; i++ {
		wg.Add(2)
		fp := fmt.Sprintf("fp-resolve-%d", i)

		go func() {
			defer wg.Done()
			c.Get(ctx, fp)
		}()

		go func() {
			defer wg.Done()
			c.MarkResolved(ctx, fp, time.Now())
		}()
	}
	wg.Wait()
}

func TestSetEmptyToolsUsed(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	result := &domain.AnalysisResult{
		AlertFingerprint: "fp-no-tools",
		Text:             "Analysis with no tools used at all",
	}

	if err := c.Set(ctx, result); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := c.Get(ctx, "fp-no-tools")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected cached result")
	}
	if len(got.ToolsUsed) != 0 {
		t.Errorf("expected empty ToolsUsed, got %v", got.ToolsUsed)
	}
}

func TestSetOverwrite(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()

	result1 := &domain.AnalysisResult{
		AlertFingerprint: "fp-overwrite",
		Text:             "First analysis text here",
		ToolsUsed:        []string{"tool-a"},
	}
	if err := c.Set(ctx, result1); err != nil {
		t.Fatalf("Set first: %v", err)
	}

	result2 := &domain.AnalysisResult{
		AlertFingerprint: "fp-overwrite",
		Text:             "Updated analysis text here",
		ToolsUsed:        []string{"tool-b", "tool-c"},
	}
	if err := c.Set(ctx, result2); err != nil {
		t.Fatalf("Set second: %v", err)
	}

	got, err := c.Get(ctx, "fp-overwrite")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected cached result")
	}
	if got.Text != "Updated analysis text here" {
		t.Errorf("expected updated text, got %q", got.Text)
	}
	if len(got.ToolsUsed) != 2 {
		t.Errorf("expected 2 tools, got %v", got.ToolsUsed)
	}
}

func TestMarkResolvedNonexistent(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	// Should not error on marking a non-existent entry.
	err = c.MarkResolved(context.Background(), "nonexistent-fp", time.Now())
	if err != nil {
		t.Errorf("MarkResolved on nonexistent: %v", err)
	}
}
