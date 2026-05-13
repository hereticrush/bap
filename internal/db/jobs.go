/*
 * internal/db/jobs.go
 *
 * Video job CRUD operations for the pipeline state machine.
 * Manages the video_jobs table lifecycle:
 *   PENDING → PROCESSING → COMPLETED | FAILED
 *
 * CreateJob atomically consumes an UNUSED prompt and creates
 * a PENDING job in a single transaction to prevent double-pickup.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package db

import (
	"crypto/rand"
	"database/sql"
	"fmt"
)

/* VideoJob represents a row in the video_jobs table. */
type VideoJob struct {
	ID                  string
	PromptID            int64
	PromptTextSnapshot  string
	PromptBuilderUsed   string
	AIProvider          string
	Status              string
	RetryCount          int
	AITaskID            string
	CloudStorageURL     string
	PublishedVideoID    string
	Metadata            string
	ErrorLog            string
	CreatedAt           string
	UpdatedAt           string
	CompletedAt         string
}

/*
 * CreateJob atomically picks an UNUSED prompt, marks it as USED,
 * and inserts a new PENDING video job in a single transaction.
 *
 * This prevents double-pickup: no two workers can claim the same
 * prompt because the SELECT + UPDATE + INSERT are transactional.
 *
 * Returns sql.ErrNoRows if no unused prompts remain.
 *
 * Transaction scope:
 *   1. SELECT the oldest UNUSED prompt (ORDER BY id ASC LIMIT 1)
 *   2. UPDATE that prompt to status = 'USED'
 *   3. INSERT a new video_jobs row with status = 'PENDING'
 *   4. COMMIT (or ROLLBACK on any error)
 */
func CreateJob(db *sql.DB, aiProvider string) (*VideoJob, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	/* Step 1: Fetch the oldest unused prompt */
	var promptID int64
	var enrichedText string
	var builderUsed string

	err = tx.QueryRow(
		`SELECT id, enriched_text, builder_used
		 FROM prompts
		 WHERE status = 'UNUSED'
		 ORDER BY id ASC
		 LIMIT 1`,
	).Scan(&promptID, &enrichedText, &builderUsed)
	if err != nil {
		return nil, err /* sql.ErrNoRows if none available */
	}

	/* Step 2: Mark the prompt as USED */
	if _, err := tx.Exec(
		"UPDATE prompts SET status = 'USED' WHERE id = ?", promptID,
	); err != nil {
		return nil, fmt.Errorf("mark prompt used: %w", err)
	}

	/* Step 3: Generate a UUID and insert the job */
	jobID, err := generateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate job ID: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT INTO video_jobs
		 (id, prompt_id, prompt_text_snapshot, prompt_builder_used, ai_provider, status)
		 VALUES (?, ?, ?, ?, ?, 'PENDING')`,
		jobID, promptID, enrichedText, builderUsed, aiProvider,
	); err != nil {
		return nil, fmt.Errorf("insert job: %w", err)
	}

	/* Step 4: Commit */
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &VideoJob{
		ID:                 jobID,
		PromptID:           promptID,
		PromptTextSnapshot: enrichedText,
		PromptBuilderUsed:  builderUsed,
		AIProvider:         aiProvider,
		Status:             "PENDING",
	}, nil
}

/*
 * SetJobProcessing marks a job as PROCESSING and stores the
 * AI provider's task ID for subsequent status polling.
 *
 * Called after RunwayAdapter.GenerateVideo() returns a task ID.
 */
func SetJobProcessing(db *sql.DB, jobID string, aiTaskID string) error {
	_, err := db.Exec(
		`UPDATE video_jobs
		 SET status = 'PROCESSING', ai_task_id = ?, updated_at = datetime('now')
		 WHERE id = ?`,
		aiTaskID, jobID,
	)
	return err
}

/*
 * SetJobCompleted marks a job as COMPLETED and stores the
 * video download URL from the AI provider.
 *
 * Called after CheckStatus() returns StatusCompleted with a VideoURL.
 */
func SetJobCompleted(db *sql.DB, jobID string, videoURL string) error {
	_, err := db.Exec(
		`UPDATE video_jobs
		 SET status = 'COMPLETED', cloud_storage_url = ?,
		     updated_at = datetime('now'), completed_at = datetime('now')
		 WHERE id = ?`,
		videoURL, jobID,
	)
	return err
}

/*
 * SetJobPublished marks a job as PUBLISHED and stores the
 * platform-specific video ID (e.g., YouTube Video ID).
 *
 * Called after Publisher.Publish() returns successfully.
 */
func SetJobPublished(db *sql.DB, jobID string, platformID string) error {
	_, err := db.Exec(
		`UPDATE video_jobs
		 SET status = 'PUBLISHED', published_video_id = ?,
		     updated_at = datetime('now')
		 WHERE id = ?`,
		platformID, jobID,
	)
	return err
}

/*
 * SetJobFailed marks a job as FAILED, stores the error log,
 * and increments the retry counter.
 *
 * Called when GenerateVideo() fails or CheckStatus() returns StatusFailed.
 */
func SetJobFailed(db *sql.DB, jobID string, errMsg string) error {
	_, err := db.Exec(
		`UPDATE video_jobs
		 SET status = 'FAILED', error_log = ?, retry_count = retry_count + 1,
		     updated_at = datetime('now')
		 WHERE id = ?`,
		errMsg, jobID,
	)
	return err
}

/*
 * GetPendingJobs fetches up to 'limit' jobs with status PENDING,
 * ordered by creation time (oldest first).
 *
 * Used by the worker to find jobs that need to be submitted
 * to the AI video provider.
 */
func GetPendingJobs(db *sql.DB, limit int) ([]VideoJob, error) {
	rows, err := db.Query(
		`SELECT id, prompt_id, prompt_text_snapshot, prompt_builder_used,
		        ai_provider, status, retry_count
		 FROM video_jobs
		 WHERE status = 'PENDING'
		 ORDER BY created_at ASC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query pending jobs: %w", err)
	}
	defer rows.Close()

	return scanJobs(rows)
}

/*
 * GetProcessingJobs fetches all jobs with status PROCESSING.
 *
 * Used by the worker to find jobs that need status polling
 * against the AI video provider (via CheckStatus).
 */
func GetProcessingJobs(db *sql.DB) ([]VideoJob, error) {
	rows, err := db.Query(
		`SELECT id, prompt_id, prompt_text_snapshot, prompt_builder_used,
		        ai_provider, status, retry_count, ai_task_id
		 FROM video_jobs
		 WHERE status = 'PROCESSING'
		 ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query processing jobs: %w", err)
	}
	defer rows.Close()

	return scanProcessingJobs(rows)
}

/*
 * scanJobs reads rows from a pending jobs query into a slice.
 * Handles the column set: id, prompt_id, prompt_text_snapshot,
 * prompt_builder_used, ai_provider, status, retry_count.
 */
func scanJobs(rows *sql.Rows) ([]VideoJob, error) {
	var jobs []VideoJob
	for rows.Next() {
		var j VideoJob
		if err := rows.Scan(
			&j.ID, &j.PromptID, &j.PromptTextSnapshot,
			&j.PromptBuilderUsed, &j.AIProvider, &j.Status, &j.RetryCount,
		); err != nil {
			return nil, fmt.Errorf("scan job row: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

/*
 * scanProcessingJobs reads rows from a processing jobs query into a slice.
 * Includes ai_task_id which is needed for CheckStatus polling.
 */
func scanProcessingJobs(rows *sql.Rows) ([]VideoJob, error) {
	var jobs []VideoJob
	for rows.Next() {
		var j VideoJob
		var aiTaskID sql.NullString
		if err := rows.Scan(
			&j.ID, &j.PromptID, &j.PromptTextSnapshot,
			&j.PromptBuilderUsed, &j.AIProvider, &j.Status,
			&j.RetryCount, &aiTaskID,
		); err != nil {
			return nil, fmt.Errorf("scan processing job row: %w", err)
		}
		if aiTaskID.Valid {
			j.AITaskID = aiTaskID.String
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

/*
 * generateUUID produces a UUID v4 string using crypto/rand.
 * Format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
 * No external dependency required.
 */
func generateUUID() (string, error) {
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	/* Set version 4 (bits 12-15 of time_hi_and_version) */
	uuid[6] = (uuid[6] & 0x0f) | 0x40

	/* Set variant RFC 4122 (bits 6-7 of clock_seq_hi) */
	uuid[8] = (uuid[8] & 0x3f) | 0x80

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16],
	), nil
}
