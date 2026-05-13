/*
 * internal/db/sqlite.go
 *
 * Opens and configures the SQLite database connection.
 * Sets WAL journal mode and busy timeout for concurrent access,
 * then runs idempotent schema migrations.
 */
package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3" /* SQLite driver registration */
)

/*
 * Open creates or opens the SQLite database at the given path.
 * It configures WAL mode and busy timeout per the architecture plan,
 * then runs schema migrations.
 *
 * The caller is responsible for closing the returned *sql.DB.
 */
func Open(dbPath string) (*sql.DB, error) {
	/* Ensure the parent directory exists */
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db directory %s: %w", dir, err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	/* Verify the connection is alive */
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	/* Configure SQLite for concurrent access (System Architecture §2) */
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("exec %q: %w", p, err)
		}
	}

	slog.Info("database opened",
		"path", dbPath,
		"journal_mode", "WAL",
		"busy_timeout_ms", 5000,
	)

	/* Run schema migrations */
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

/*
 * migrate runs idempotent CREATE TABLE IF NOT EXISTS statements.
 * Safe to call on every startup — existing tables are untouched.
 *
 * Schema sources:
 *   - prompts:       Prompt Builder plan §4.1
 *   - prompt_config: Prompt Builder plan §4.2
 *   - video_jobs:    System Architecture §5 + Prompt Builder §4.3
 */
func migrate(db *sql.DB) error {
	statements := []string{
		/* prompts — stores LLM-enriched video generation prompts */
		`CREATE TABLE IF NOT EXISTS prompts (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			seed_text     TEXT    NOT NULL,
			enriched_text TEXT    NOT NULL,
			status        TEXT    NOT NULL DEFAULT 'UNUSED',
			tokens_used   INTEGER NOT NULL DEFAULT 0,
			builder_used  TEXT    NOT NULL DEFAULT '',
			metadata      TEXT
		)`,

		/* prompt_config — per-batch metadata (system prompt, target provider) */
		`CREATE TABLE IF NOT EXISTS prompt_config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,

		/* video_jobs — tracks every video through the pipeline state machine */
		`CREATE TABLE IF NOT EXISTS video_jobs (
			id                    TEXT PRIMARY KEY,
			prompt_id             INTEGER,
			prompt_text_snapshot  TEXT    NOT NULL,
			prompt_builder_used   TEXT,
			ai_provider           TEXT    NOT NULL,
			status                TEXT    NOT NULL DEFAULT 'PENDING',
			retry_count           INTEGER NOT NULL DEFAULT 0,
			ai_task_id            TEXT,
			cloud_storage_url     TEXT,
			published_video_id    TEXT,
			metadata              TEXT,
			error_log             TEXT,
			created_at            TEXT    NOT NULL DEFAULT (datetime('now')),
			updated_at            TEXT    NOT NULL DEFAULT (datetime('now')),
			completed_at          TEXT,
			FOREIGN KEY (prompt_id) REFERENCES prompts(id)
		)`,

		/* Index on video_jobs.status for the recovery sweep query */
		`CREATE INDEX IF NOT EXISTS idx_video_jobs_status
			ON video_jobs(status)`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec migration: %w\nSQL: %s", err, stmt)
		}
	}

	slog.Info("database migrations complete",
		"tables", "prompts, prompt_config, video_jobs",
	)
	return nil
}

/*
 * CountJobsByStatus returns the number of video_jobs rows
 * with the given status value. Uses the idx_video_jobs_status
 * index for efficient lookup.
 *
 * Useful for the /healthz endpoint to surface pipeline state
 * (e.g., how many jobs are PENDING or FAILED).
 */
func CountJobsByStatus(db *sql.DB, status string) (int, error) {
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM video_jobs WHERE status = ?", status,
	).Scan(&count)
	return count, err
}
