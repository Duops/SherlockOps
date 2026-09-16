package cache

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

const defaultEnvName = "default"

func createEventsTable(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS alert_events (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			fingerprint TEXT NOT NULL,
			alert_name  TEXT NOT NULL DEFAULT '',
			environment TEXT NOT NULL DEFAULT '',
			source      TEXT NOT NULL DEFAULT '',
			severity    TEXT NOT NULL DEFAULT '',
			status      TEXT NOT NULL DEFAULT '',
			received_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_alert_events_received_at ON alert_events(received_at)`,
		`CREATE INDEX IF NOT EXISTS idx_alert_events_env ON alert_events(environment, received_at)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("cache: create alert_events: %w", err)
		}
	}
	return nil
}

// RecordEvent appends one row for a received alert notification.
func (c *SQLiteCache) RecordEvent(ctx context.Context, alert *domain.Alert) error {
	if alert == nil {
		return fmt.Errorf("cache: RecordEvent: nil alert")
	}
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO alert_events (fingerprint, alert_name, environment, source, severity, status, received_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		alert.Fingerprint, alert.Name, alert.Environment, alert.Source,
		string(alert.Severity), string(alert.Status), time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("cache: record event: %w", err)
	}
	return nil
}

// CleanupEvents deletes events older than the cutoff and returns the number removed.
func (c *SQLiteCache) CleanupEvents(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := c.db.ExecContext(ctx,
		"DELETE FROM alert_events WHERE received_at < ?",
		olderThan.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("cache: cleanup events: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

type eventBucket struct {
	name, env string
	firing    int
	resolved  int
}

// AlertStats aggregates alert_events since the given time; env "" means all, "default" means no X-Environment.
func (c *SQLiteCache) AlertStats(ctx context.Context, since time.Time, env string) (*domain.AlertStats, error) {
	now := time.Now().UTC()
	stats := &domain.AlertStats{
		Since:         since.UTC(),
		Until:         now,
		Days:          int(now.Sub(since).Hours()/24 + 0.5),
		Environment:   env,
		TopAlerts:     []domain.AlertCount{},
		ByEnvironment: []domain.EnvCount{},
		Environments:  []string{},
	}
	if stats.Days < 1 {
		stats.Days = 1
	}

	envRows, err := c.db.QueryContext(ctx, `SELECT DISTINCT environment FROM alert_events ORDER BY environment`)
	if err != nil {
		return nil, fmt.Errorf("cache: stats environments: %w", err)
	}
	for envRows.Next() {
		var e string
		if err := envRows.Scan(&e); err != nil {
			envRows.Close()
			return nil, fmt.Errorf("cache: stats environments scan: %w", err)
		}
		stats.Environments = append(stats.Environments, displayEnv(e))
	}
	envRows.Close()
	sort.Strings(stats.Environments)

	query := `SELECT alert_name, environment, status, COUNT(*)
		FROM alert_events WHERE received_at >= ?`
	args := []interface{}{since.UTC().Format(time.RFC3339)}
	if env != "" {
		query += " AND environment = ?"
		if env == defaultEnvName {
			args = append(args, "")
		} else {
			args = append(args, env)
		}
	}
	query += " GROUP BY alert_name, environment, status"

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("cache: stats query: %w", err)
	}
	defer rows.Close()

	buckets := map[string]*eventBucket{}
	for rows.Next() {
		var name, e, status string
		var n int
		if err := rows.Scan(&name, &e, &status, &n); err != nil {
			return nil, fmt.Errorf("cache: stats scan: %w", err)
		}
		key := name + "\x00" + e
		b, ok := buckets[key]
		if !ok {
			b = &eventBucket{name: name, env: displayEnv(e)}
			buckets[key] = b
		}
		if status == string(domain.StatusResolved) {
			b.resolved += n
		} else {
			b.firing += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cache: stats rows: %w", err)
	}

	aggregate(stats, buckets)
	return stats, nil
}

func aggregate(stats *domain.AlertStats, buckets map[string]*eventBucket) {
	byName := map[string]*domain.AlertCount{}
	nameEnvs := map[string]map[string]struct{}{}
	byEnv := map[string]*domain.EnvCount{}
	envTop := map[string]map[string]int{}

	for _, b := range buckets {
		total := b.firing + b.resolved
		stats.Total += total
		stats.Firing += b.firing
		stats.Resolved += b.resolved

		a, ok := byName[b.name]
		if !ok {
			a = &domain.AlertCount{Name: b.name}
			byName[b.name] = a
			nameEnvs[b.name] = map[string]struct{}{}
		}
		a.Count += total
		a.Firing += b.firing
		a.Resolved += b.resolved
		nameEnvs[b.name][b.env] = struct{}{}

		e, ok := byEnv[b.env]
		if !ok {
			e = &domain.EnvCount{Environment: b.env}
			byEnv[b.env] = e
			envTop[b.env] = map[string]int{}
		}
		e.Count += total
		e.Firing += b.firing
		e.Resolved += b.resolved
		e.UniqueAlerts++
		envTop[b.env][b.name] += total
	}

	stats.UniqueAlerts = len(byName)
	if stats.Total > 0 {
		stats.PerDay = float64(stats.Total) / float64(stats.Days)
	}

	for name, a := range byName {
		for e := range nameEnvs[name] {
			a.Environments = append(a.Environments, e)
		}
		sort.Strings(a.Environments)
		a.Share = share(a.Count, stats.Total)
		stats.TopAlerts = append(stats.TopAlerts, *a)
	}
	sort.Slice(stats.TopAlerts, func(i, j int) bool {
		if stats.TopAlerts[i].Count != stats.TopAlerts[j].Count {
			return stats.TopAlerts[i].Count > stats.TopAlerts[j].Count
		}
		return stats.TopAlerts[i].Name < stats.TopAlerts[j].Name
	})
	stats.Top3Share = topShare(stats.TopAlerts, 3, stats.Total)
	stats.Top8Share = topShare(stats.TopAlerts, 8, stats.Total)
	stats.Top30Share = topShare(stats.TopAlerts, 30, stats.Total)
	if len(stats.TopAlerts) > 50 {
		stats.TopAlerts = stats.TopAlerts[:50]
	}

	for env, e := range byEnv {
		e.Share = share(e.Count, stats.Total)
		bestName, best := "", -1
		for name, n := range envTop[env] {
			if n > best || (n == best && name < bestName) {
				bestName, best = name, n
			}
		}
		e.TopAlert = bestName
		stats.ByEnvironment = append(stats.ByEnvironment, *e)
	}
	sort.Slice(stats.ByEnvironment, func(i, j int) bool {
		if stats.ByEnvironment[i].Count != stats.ByEnvironment[j].Count {
			return stats.ByEnvironment[i].Count > stats.ByEnvironment[j].Count
		}
		return stats.ByEnvironment[i].Environment < stats.ByEnvironment[j].Environment
	})
}

func topShare(alerts []domain.AlertCount, n, total int) float64 {
	sum := 0
	for i := 0; i < n && i < len(alerts); i++ {
		sum += alerts[i].Count
	}
	return share(sum, total)
}

func share(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}

func displayEnv(e string) string {
	if e == "" {
		return defaultEnvName
	}
	return e
}
