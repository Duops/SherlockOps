package cache

import (
	"context"
	"testing"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

func recordN(t *testing.T, c *SQLiteCache, name, env string, status domain.AlertStatus, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		err := c.RecordEvent(context.Background(), &domain.Alert{
			Fingerprint: name + "-" + env,
			Name:        name,
			Environment: env,
			Source:      "alertmanager",
			Severity:    domain.SeverityWarning,
			Status:      status,
		})
		if err != nil {
			t.Fatalf("RecordEvent: %v", err)
		}
	}
}

func TestAlertStats_AggregatesAcrossEnvironments(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	recordN(t, c, "TargetDown", "prod", domain.StatusFiring, 6)
	recordN(t, c, "TargetDown", "prod", domain.StatusResolved, 2)
	recordN(t, c, "CPUThrottlingHigh", "dev", domain.StatusFiring, 4)
	recordN(t, c, "KubePodCrashLooping", "", domain.StatusFiring, 1)
	recordN(t, c, "TargetDown", "dev", domain.StatusFiring, 3)

	since := time.Now().Add(-24 * time.Hour)
	stats, err := c.AlertStats(context.Background(), since, "")
	if err != nil {
		t.Fatalf("AlertStats: %v", err)
	}

	if stats.Total != 16 || stats.Firing != 14 || stats.Resolved != 2 {
		t.Errorf("total/firing/resolved = %d/%d/%d, want 16/14/2", stats.Total, stats.Firing, stats.Resolved)
	}
	if stats.UniqueAlerts != 3 {
		t.Errorf("unique alerts = %d, want 3", stats.UniqueAlerts)
	}
	if len(stats.TopAlerts) != 3 || stats.TopAlerts[0].Name != "TargetDown" || stats.TopAlerts[0].Count != 11 {
		t.Fatalf("unexpected top alerts: %+v", stats.TopAlerts)
	}
	if len(stats.TopAlerts[0].Environments) != 2 {
		t.Errorf("TargetDown environments = %v, want [dev prod]", stats.TopAlerts[0].Environments)
	}
	if stats.Top3Share != 100 {
		t.Errorf("top3 share = %v, want 100", stats.Top3Share)
	}
	if len(stats.ByEnvironment) != 3 || stats.ByEnvironment[0].Environment != "prod" || stats.ByEnvironment[0].Count != 8 {
		t.Fatalf("unexpected by-environment: %+v", stats.ByEnvironment)
	}
	if stats.ByEnvironment[0].TopAlert != "TargetDown" {
		t.Errorf("prod top alert = %q, want TargetDown", stats.ByEnvironment[0].TopAlert)
	}
	// Empty environment is reported as "default".
	found := false
	for _, e := range stats.ByEnvironment {
		if e.Environment == "default" && e.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected default environment with 1 event, got %+v", stats.ByEnvironment)
	}
	if len(stats.Environments) != 3 {
		t.Errorf("environments = %v, want 3 entries", stats.Environments)
	}
}

func TestAlertStats_FilterByEnvironment(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	recordN(t, c, "TargetDown", "prod", domain.StatusFiring, 5)
	recordN(t, c, "TargetDown", "dev", domain.StatusFiring, 3)
	recordN(t, c, "Other", "", domain.StatusFiring, 1)

	stats, err := c.AlertStats(context.Background(), time.Now().Add(-time.Hour), "dev")
	if err != nil {
		t.Fatalf("AlertStats: %v", err)
	}
	if stats.Total != 3 || stats.Environment != "dev" {
		t.Errorf("total = %d env = %q, want 3/dev", stats.Total, stats.Environment)
	}
	// The environment list always covers all known environments so the UI filter stays populated.
	if len(stats.Environments) != 3 {
		t.Errorf("environments = %v, want 3 entries", stats.Environments)
	}

	def, err := c.AlertStats(context.Background(), time.Now().Add(-time.Hour), "default")
	if err != nil {
		t.Fatalf("AlertStats default: %v", err)
	}
	if def.Total != 1 {
		t.Errorf("default total = %d, want 1", def.Total)
	}
}

func TestAlertStats_WindowAndCleanup(t *testing.T) {
	c, err := New(tempDB(t), time.Hour, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	recordN(t, c, "Fresh", "prod", domain.StatusFiring, 2)
	old := time.Now().Add(-10 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := c.db.Exec(`INSERT INTO alert_events (fingerprint, alert_name, environment, source, severity, status, received_at)
		VALUES ('fp', 'Stale', 'prod', 'alertmanager', 'warning', 'firing', ?)`, old); err != nil {
		t.Fatal(err)
	}

	stats, err := c.AlertStats(context.Background(), time.Now().Add(-7*24*time.Hour), "")
	if err != nil {
		t.Fatalf("AlertStats: %v", err)
	}
	if stats.Total != 2 || stats.Days != 7 {
		t.Errorf("total = %d days = %d, want 2/7", stats.Total, stats.Days)
	}

	n, err := c.CleanupEvents(context.Background(), time.Now().Add(-5*24*time.Hour))
	if err != nil || n != 1 {
		t.Errorf("CleanupEvents = %d, %v; want 1, nil", n, err)
	}
}
