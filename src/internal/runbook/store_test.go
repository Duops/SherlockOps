package runbook

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Duops/SherlockOps/internal/domain"
)

func setupTestDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadMultipleFiles(t *testing.T) {
	dir := setupTestDir(t, map[string]string{
		"high-cpu.md": `---
title: "High CPU"
alerts:
  - "HighCPU*"
priority: 10
---
Check CPU usage.`,
		"disk-full.md": `---
title: "Disk Full"
alerts:
  - "DiskFull"
priority: 5
---
Check disk usage.`,
		"not-markdown.txt": `This should be ignored.`,
	})

	store, err := NewStore(dir, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}

	store.mu.RLock()
	count := len(store.runbooks)
	store.mu.RUnlock()

	if count != 2 {
		t.Fatalf("expected 2 runbooks, got %d", count)
	}
}

func TestMatchExactAlertName(t *testing.T) {
	dir := setupTestDir(t, map[string]string{
		"disk.md": `---
title: "Disk Full"
alerts:
  - "DiskFull"
priority: 1
---
Check disk.`,
	})

	store, err := NewStore(dir, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}

	alert := &domain.Alert{Name: "DiskFull", Labels: map[string]string{}}
	matches := store.Match(alert)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Title != "Disk Full" {
		t.Fatalf("expected title 'Disk Full', got %q", matches[0].Title)
	}
}

func TestMatchGlobPattern(t *testing.T) {
	dir := setupTestDir(t, map[string]string{
		"cpu.md": `---
title: "CPU Issues"
alerts:
  - "HighCPU*"
priority: 1
---
CPU runbook.`,
	})

	store, err := NewStore(dir, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		alertName string
		want      int
	}{
		{"HighCPUWarning", 1},
		{"HighCPU", 1},
		{"HighCPUCritical", 1},
		{"LowCPU", 0},
	}

	for _, tt := range tests {
		alert := &domain.Alert{Name: tt.alertName, Labels: map[string]string{}}
		matches := store.Match(alert)
		if len(matches) != tt.want {
			t.Errorf("alert %q: expected %d matches, got %d", tt.alertName, tt.want, len(matches))
		}
	}
}

func TestMatchLabels(t *testing.T) {
	dir := setupTestDir(t, map[string]string{
		"critical.md": `---
title: "Critical Alerts"
alerts:
  - "HighCPU*"
labels:
  severity: critical
priority: 10
---
Critical runbook.`,
	})

	store, err := NewStore(dir, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}

	// Matches: alertname + label
	alert := &domain.Alert{
		Name:   "HighCPUWarning",
		Labels: map[string]string{"severity": "critical", "team": "platform"},
	}
	matches := store.Match(alert)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}

	// Does not match: alertname ok but label mismatch
	alert2 := &domain.Alert{
		Name:   "HighCPUWarning",
		Labels: map[string]string{"severity": "warning"},
	}
	matches2 := store.Match(alert2)
	if len(matches2) != 0 {
		t.Fatalf("expected 0 matches for wrong label, got %d", len(matches2))
	}
}

func TestPrioritySorting(t *testing.T) {
	dir := setupTestDir(t, map[string]string{
		"low.md": `---
title: "Low Priority"
alerts:
  - "TestAlert"
priority: 1
---
Low.`,
		"high.md": `---
title: "High Priority"
alerts:
  - "TestAlert"
priority: 100
---
High.`,
		"mid.md": `---
title: "Mid Priority"
alerts:
  - "TestAlert"
priority: 50
---
Mid.`,
	})

	store, err := NewStore(dir, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}

	alert := &domain.Alert{Name: "TestAlert", Labels: map[string]string{}}
	matches := store.Match(alert)
	if len(matches) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(matches))
	}
	if matches[0].Title != "High Priority" {
		t.Errorf("expected first match to be 'High Priority', got %q", matches[0].Title)
	}
	if matches[1].Title != "Mid Priority" {
		t.Errorf("expected second match to be 'Mid Priority', got %q", matches[1].Title)
	}
	if matches[2].Title != "Low Priority" {
		t.Errorf("expected third match to be 'Low Priority', got %q", matches[2].Title)
	}
}

func TestNoMatch(t *testing.T) {
	dir := setupTestDir(t, map[string]string{
		"cpu.md": `---
title: "CPU"
alerts:
  - "HighCPU"
priority: 1
---
CPU.`,
	})

	store, err := NewStore(dir, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}

	alert := &domain.Alert{Name: "DiskFull", Labels: map[string]string{}}
	matches := store.Match(alert)
	if len(matches) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(matches))
	}
}

func TestMalformedFrontmatterSkipped(t *testing.T) {
	dir := setupTestDir(t, map[string]string{
		"good.md": `---
title: "Good"
alerts:
  - "GoodAlert"
priority: 1
---
Good content.`,
		"bad.md": `---
title: [[[invalid yaml
---
Bad content.`,
		"no-frontmatter.md": `Just plain markdown without frontmatter.`,
	})

	store, err := NewStore(dir, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}

	store.mu.RLock()
	count := len(store.runbooks)
	store.mu.RUnlock()

	if count != 1 {
		t.Fatalf("expected 1 runbook (malformed ones skipped), got %d", count)
	}
}

func TestReload(t *testing.T) {
	dir := setupTestDir(t, map[string]string{
		"a.md": `---
title: "A"
alerts:
  - "AlertA"
priority: 1
---
A content.`,
	})

	store, err := NewStore(dir, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}

	store.mu.RLock()
	if len(store.runbooks) != 1 {
		t.Fatalf("expected 1 runbook, got %d", len(store.runbooks))
	}
	store.mu.RUnlock()

	// Add a second file and reload.
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte(`---
title: "B"
alerts:
  - "AlertB"
priority: 2
---
B content.`), 0644); err != nil {
		t.Fatal(err)
	}

	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}

	store.mu.RLock()
	count := len(store.runbooks)
	store.mu.RUnlock()

	if count != 2 {
		t.Fatalf("expected 2 runbooks after reload, got %d", count)
	}
}

func TestFormatForPrompt(t *testing.T) {
	runbooks := []Runbook{
		{Title: "High CPU", Content: "Check top."},
		{Title: "Disk Full", Content: "Check df -h."},
	}

	result := FormatForPrompt(runbooks)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !contains(result, "<runbooks>") || !contains(result, "</runbooks>") {
		t.Error("expected runbooks XML tags")
	}
	if !contains(result, "## High CPU") {
		t.Error("expected High CPU title")
	}
	if !contains(result, "## Disk Full") {
		t.Error("expected Disk Full title")
	}
}

func TestFormatForPromptEmpty(t *testing.T) {
	result := FormatForPrompt(nil)
	if result != "" {
		t.Fatalf("expected empty string for no runbooks, got %q", result)
	}
}

func TestNewStoreEmptyDir(t *testing.T) {
	_, err := NewStore("", slog.Default())
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
