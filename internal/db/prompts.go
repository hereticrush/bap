/*
 * internal/db/prompts.go
 *
 * Prompt CRUD operations for the video pipeline.
 * BuildBatch manages its own transactional inserts; these functions
 * serve the pipeline (fetching unused prompts) and the health
 * endpoint (counting remaining prompts).
 */
package db

import (
	"database/sql"
	"fmt"
)

/* Prompt represents a row in the prompts table. */
type Prompt struct {
	ID           int64
	SeedText     string
	EnrichedText string
	Status       string
	TokensUsed   int
	BuilderUsed  string
}

/*
 * GetUnusedPrompt fetches a single prompt with status 'UNUSED'
 * and atomically marks it as 'PROCESSING' to prevent double-pickup
 * by concurrent workers.
 *
 * Returns sql.ErrNoRows if no unused prompts remain.
 */
func GetUnusedPrompt(db *sql.DB) (*Prompt, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	p := &Prompt{}
	err = tx.QueryRow(
		`SELECT id, seed_text, enriched_text, status, tokens_used, builder_used
		 FROM prompts
		 WHERE status = 'UNUSED'
		 ORDER BY id ASC
		 LIMIT 1`,
	).Scan(&p.ID, &p.SeedText, &p.EnrichedText, &p.Status, &p.TokensUsed, &p.BuilderUsed)
	if err != nil {
		return nil, err /* sql.ErrNoRows if none available */
	}

	if _, err := tx.Exec(
		"UPDATE prompts SET status = 'PROCESSING' WHERE id = ?", p.ID,
	); err != nil {
		return nil, fmt.Errorf("mark processing: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	p.Status = "PROCESSING"
	return p, nil
}

/*
 * MarkPromptUsed sets the prompt status to 'USED' after
 * its enriched text has been copied into a video_jobs row.
 */
func MarkPromptUsed(db *sql.DB, promptID int64) error {
	_, err := db.Exec(
		"UPDATE prompts SET status = 'USED' WHERE id = ?", promptID,
	)
	return err
}

/*
 * CountByStatus returns the number of prompts with the given status.
 * Useful for the /healthz endpoint ("prompts_remaining": N).
 */
func CountByStatus(db *sql.DB, status string) (int, error) {
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM prompts WHERE status = ?", status,
	).Scan(&count)
	return count, err
}
