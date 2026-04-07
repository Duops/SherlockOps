package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/Duops/SherlockOps/internal/cache"
	"github.com/Duops/SherlockOps/internal/domain"
	"github.com/Duops/SherlockOps/internal/webui"
)

func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{" debug ", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"unknown-level", slog.LevelInfo},
	}
	for _, tc := range cases {
		if got := parseLogLevel(tc.in); got != tc.want {
			t.Errorf("parseLogLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestPendingListerAdapter(t *testing.T) {
	c, err := cache.New(t.TempDir()+"/test.db", time.Hour, 5)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	ref := &domain.MessageRef{Messenger: "slack", Channel: "C1", MessageID: "100"}
	a := &domain.Alert{Name: "X", Fingerprint: "fp1", RawText: "raw"}
	if err := c.SavePending(ctx, ref, a); err != nil {
		t.Fatalf("SavePending: %v", err)
	}

	adapter := pendingListerAdapter{c: c}
	items, err := adapter.ListPending(ctx, 10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Alert == nil || items[0].Alert.Fingerprint != "fp1" {
		t.Errorf("alert not preserved through adapter: %+v", items[0])
	}
	if items[0].CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be populated")
	}

	// Adapter must satisfy webui.PendingLister.
	var _ webui.PendingLister = adapter
}
