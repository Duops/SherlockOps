package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"

	_ "modernc.org/sqlite"
)

// SQLiteCache implements domain.Cache using a local SQLite database.
type SQLiteCache struct {
	db        *sql.DB
	ttl       time.Duration
	minLength int
}

// New opens (or creates) an SQLite database at dbPath and returns a ready cache.
// ttl controls how long cached entries are considered valid.
// minLength is the minimum analysis text length required for caching.
func New(dbPath string, ttl time.Duration, minLength int) (*SQLiteCache, error) {
	// Sanitize the database path to prevent directory traversal.
	dbPath = filepath.Clean(dbPath)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("cache: open db: %w", err)
	}

	// Enable WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("cache: set WAL: %w", err)
	}

	// Set busy timeout to avoid SQLITE_BUSY under concurrent writes.
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("cache: set busy_timeout: %w", err)
	}

	// Limit to 1 open connection — SQLite does not support concurrent writers.
	// WAL mode allows concurrent reads, but writes must be serialized.
	db.SetMaxOpenConns(1)

	createSQL := `CREATE TABLE IF NOT EXISTS alerts_cache (
		fingerprint  TEXT PRIMARY KEY,
		analysis_text TEXT,
		tools_used   TEXT,
		created_at   TEXT,
		resolved_at  TEXT
	)`
	if _, err := db.Exec(createSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("cache: create table: %w", err)
	}

	pendingSQL := `CREATE TABLE IF NOT EXISTS pending_alerts (
		key        TEXT PRIMARY KEY,
		alert_json TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`
	if _, err := db.Exec(pendingSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("cache: create pending table: %w", err)
	}

	return &SQLiteCache{
		db:        db,
		ttl:       ttl,
		minLength: minLength,
	}, nil
}

// Get returns a cached analysis result or nil if not found or expired.
func (c *SQLiteCache) Get(ctx context.Context, fingerprint string) (*domain.AnalysisResult, error) {
	row := c.db.QueryRowContext(ctx,
		"SELECT analysis_text, tools_used, created_at, resolved_at FROM alerts_cache WHERE fingerprint = ?",
		fingerprint,
	)

	var (
		text       string
		toolsRaw   string
		createdRaw string
		resolvedRaw sql.NullString
	)
	if err := row.Scan(&text, &toolsRaw, &createdRaw, &resolvedRaw); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("cache: get: %w", err)
	}

	createdAt, err := time.Parse(time.RFC3339, createdRaw)
	if err != nil {
		return nil, fmt.Errorf("cache: parse created_at: %w", err)
	}

	if time.Since(createdAt) > c.ttl {
		return nil, nil
	}

	result := &domain.AnalysisResult{
		AlertFingerprint: fingerprint,
		Text:             text,
		CachedAt:         createdAt,
	}

	if toolsRaw != "" {
		result.ToolsUsed = strings.Split(toolsRaw, ",")
	}

	if resolvedRaw.Valid && resolvedRaw.String != "" {
		t, err := time.Parse(time.RFC3339, resolvedRaw.String)
		if err == nil {
			result.ResolvedAt = &t
		}
	}

	return result, nil
}

// Set upserts an analysis result. It is a no-op when the text is shorter than minLength.
func (c *SQLiteCache) Set(ctx context.Context, result *domain.AnalysisResult) error {
	if len(result.Text) < c.minLength {
		return nil
	}

	toolsStr := strings.Join(result.ToolsUsed, ",")
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := c.db.ExecContext(ctx,
		`INSERT INTO alerts_cache (fingerprint, analysis_text, tools_used, created_at, resolved_at)
		 VALUES (?, ?, ?, ?, NULL)
		 ON CONFLICT(fingerprint) DO UPDATE SET
		   analysis_text = excluded.analysis_text,
		   tools_used    = excluded.tools_used,
		   created_at    = excluded.created_at`,
		result.AlertFingerprint, result.Text, toolsStr, now,
	)
	if err != nil {
		return fmt.Errorf("cache: set: %w", err)
	}
	return nil
}

// MarkResolved updates the resolved_at timestamp for a cached entry.
func (c *SQLiteCache) MarkResolved(ctx context.Context, fingerprint string, resolvedAt time.Time) error {
	_, err := c.db.ExecContext(ctx,
		"UPDATE alerts_cache SET resolved_at = ? WHERE fingerprint = ?",
		resolvedAt.UTC().Format(time.RFC3339), fingerprint,
	)
	if err != nil {
		return fmt.Errorf("cache: mark resolved: %w", err)
	}
	return nil
}

// List returns recent cache entries ordered by created_at DESC.
// It returns the matching entries, the total count, and any error.
func (c *SQLiteCache) List(ctx context.Context, limit int, offset int) ([]*domain.AnalysisResult, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts_cache").Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("cache: list count: %w", err)
	}

	rows, err := c.db.QueryContext(ctx,
		"SELECT fingerprint, analysis_text, tools_used, created_at, resolved_at FROM alerts_cache ORDER BY created_at DESC LIMIT ? OFFSET ?",
		limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("cache: list query: %w", err)
	}
	defer rows.Close()

	var results []*domain.AnalysisResult
	for rows.Next() {
		var (
			fingerprint string
			text        string
			toolsRaw    string
			createdRaw  string
			resolvedRaw sql.NullString
		)
		if err := rows.Scan(&fingerprint, &text, &toolsRaw, &createdRaw, &resolvedRaw); err != nil {
			return nil, 0, fmt.Errorf("cache: list scan: %w", err)
		}

		result := &domain.AnalysisResult{
			AlertFingerprint: fingerprint,
			Text:             text,
		}

		if createdAt, err := time.Parse(time.RFC3339, createdRaw); err == nil {
			result.CachedAt = createdAt
		}

		if toolsRaw != "" {
			result.ToolsUsed = strings.Split(toolsRaw, ",")
		}

		if resolvedRaw.Valid && resolvedRaw.String != "" {
			if t, err := time.Parse(time.RFC3339, resolvedRaw.String); err == nil {
				result.ResolvedAt = &t
			}
		}

		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("cache: list rows: %w", err)
	}

	return results, total, nil
}

// Stats returns aggregate statistics about the cache.
func (c *SQLiteCache) Stats(ctx context.Context) (*domain.CacheStats, error) {
	var stats domain.CacheStats

	err := c.db.QueryRowContext(ctx,
		`SELECT
			COUNT(*),
			COUNT(CASE WHEN resolved_at IS NOT NULL AND resolved_at != '' THEN 1 END),
			COALESCE(AVG(LENGTH(analysis_text)), 0)
		FROM alerts_cache`,
	).Scan(&stats.TotalCount, &stats.ResolvedCount, &stats.AvgTextLength)
	if err != nil {
		return nil, fmt.Errorf("cache: stats: %w", err)
	}

	return &stats, nil
}

// Close releases the database connection.
func (c *SQLiteCache) Close() error {
	return c.db.Close()
}

// pendingKey builds the storage key for a pending alert.
func pendingKey(messenger, channel, messageID string) string {
	return messenger + "\x1f" + channel + "\x1f" + messageID
}

// SavePending stores the raw alert under (messenger, channel, message_id) so that
// a later @bot mention referencing this message can recover it.
func (c *SQLiteCache) SavePending(ctx context.Context, ref *domain.MessageRef, alert *domain.Alert) error {
	if ref == nil || alert == nil {
		return fmt.Errorf("cache: SavePending: nil ref or alert")
	}
	data, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("cache: SavePending marshal: %w", err)
	}
	key := pendingKey(ref.Messenger, ref.Channel, ref.MessageID)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = c.db.ExecContext(ctx,
		`INSERT INTO pending_alerts (key, alert_json, created_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		   alert_json = excluded.alert_json,
		   created_at = excluded.created_at`,
		key, string(data), now,
	)
	if err != nil {
		return fmt.Errorf("cache: SavePending: %w", err)
	}
	return nil
}

// GetPending returns the alert previously stored for this messenger/channel/message_id,
// or (nil, nil) if absent.
func (c *SQLiteCache) GetPending(ctx context.Context, messenger, channel, messageID string) (*domain.Alert, error) {
	row := c.db.QueryRowContext(ctx,
		"SELECT alert_json FROM pending_alerts WHERE key = ?",
		pendingKey(messenger, channel, messageID),
	)
	var data string
	if err := row.Scan(&data); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("cache: GetPending: %w", err)
	}
	var alert domain.Alert
	if err := json.Unmarshal([]byte(data), &alert); err != nil {
		return nil, fmt.Errorf("cache: GetPending unmarshal: %w", err)
	}
	return &alert, nil
}

// DeletePending removes the entry for this messenger/channel/message_id.
func (c *SQLiteCache) DeletePending(ctx context.Context, messenger, channel, messageID string) error {
	_, err := c.db.ExecContext(ctx,
		"DELETE FROM pending_alerts WHERE key = ?",
		pendingKey(messenger, channel, messageID),
	)
	if err != nil {
		return fmt.Errorf("cache: DeletePending: %w", err)
	}
	return nil
}
