package cache

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

func createReviewsTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS alert_reviews (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		environment   TEXT NOT NULL DEFAULT '',
		since         TEXT NOT NULL,
		until         TEXT NOT NULL,
		review_text   TEXT NOT NULL,
		model         TEXT NOT NULL DEFAULT '',
		input_tokens  INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cost_usd      REAL NOT NULL DEFAULT 0,
		created_at    TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("cache: create alert_reviews: %w", err)
	}
	return nil
}

// SaveReview inserts a review and sets its ID and CreatedAt.
func (c *SQLiteCache) SaveReview(ctx context.Context, r *domain.AlertReview) error {
	if r == nil {
		return fmt.Errorf("cache: SaveReview: nil review")
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	res, err := c.db.ExecContext(ctx,
		`INSERT INTO alert_reviews (environment, since, until, review_text, model, input_tokens, output_tokens, cost_usd, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Environment, r.Since.UTC().Format(time.RFC3339), r.Until.UTC().Format(time.RFC3339),
		r.Text, r.Model, r.InputTokens, r.OutputTokens, r.CostUSD, r.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("cache: save review: %w", err)
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

// LatestReview returns the newest review for env or nil when none exists.
func (c *SQLiteCache) LatestReview(ctx context.Context, env string) (*domain.AlertReview, error) {
	row := c.db.QueryRowContext(ctx,
		`SELECT id, environment, since, until, review_text, model, input_tokens, output_tokens, cost_usd, created_at
		 FROM alert_reviews WHERE environment = ? ORDER BY created_at DESC, id DESC LIMIT 1`, env)
	var r domain.AlertReview
	var since, until, created string
	if err := row.Scan(&r.ID, &r.Environment, &since, &until, &r.Text, &r.Model,
		&r.InputTokens, &r.OutputTokens, &r.CostUSD, &created); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("cache: latest review: %w", err)
	}
	r.Since, _ = time.Parse(time.RFC3339, since)
	r.Until, _ = time.Parse(time.RFC3339, until)
	r.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return &r, nil
}
