/*
 * internal/batch/build.go
 *
 * Implements the atomic batch prompt enrichment.
 * Wraps the entire operation — table truncation, LLM enrichment,
 * and row insertion — in a single SQLite transaction.
 *
 * If any seed fails enrichment, the entire batch rolls back
 * and the database remains in its previous state.
 */
package batch

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/hereticrush/bap/internal/adapter/prompt"
	"github.com/hereticrush/bap/internal/db"
)

/*
 * BuildBatch orchestrates the atomic batch prompt enrichment.
 * It opens a single SQLite transaction, iterates all seeds through
 * the active AIPromptBuilder, and either commits all results or
 * rolls back on any failure.
 *
 * Transaction scope:
 *   1. DELETE all existing prompts
 *   2. Store batch-level config (system_prompt, target_provider)
 *   3. For each seed: call builder.BuildPrompt(), INSERT result
 *   4. COMMIT (or ROLLBACK on any error)
 */
func BuildBatch(ctx context.Context, sqliteDB *sql.DB, builder prompt.AIPromptBuilder, batch prompt.BatchInput) error {
	tx, err := sqliteDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() /* no-op if Commit() succeeds */

	/* Truncate existing prompts */
	if _, err := tx.ExecContext(ctx, "DELETE FROM prompts"); err != nil {
		return fmt.Errorf("truncate prompts: %w", err)
	}

	/* Store batch-level config */
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO prompt_config (key, value)
		 VALUES ('system_prompt', ?), ('target_provider', ?)`,
		batch.SystemPrompt, batch.TargetProvider,
	); err != nil {
		return fmt.Errorf("store config: %w", err)
	}

	/* Enrich each seed — any failure aborts the entire batch */
	for i, seed := range batch.Seeds {
		slog.Info("enriching seed",
			"index", i+1,
			"total", len(batch.Seeds),
			"builder", builder.Name(),
		)

		result, err := builder.BuildPrompt(ctx, prompt.PromptBuildRequest{
			SystemPrompt:   batch.SystemPrompt,
			SeedPrompt:     seed.SeedText,
			TargetProvider: batch.TargetProvider,
			Metadata:       seed.Metadata,
		})
		if err != nil {
			slog.Error("LLM failed — rolling back entire batch",
				"index", i+1,
				"seed", seed.SeedText,
				"error", err,
			)
			return fmt.Errorf("seed #%d failed: %w", i+1, err)
		}

		metaJSON, err := db.MetadataJSON(seed.Metadata)
		if err != nil {
			return fmt.Errorf("marshal metadata for seed #%d: %w", i+1, err)
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO prompts (seed_text, enriched_text, status, tokens_used, builder_used, metadata)
			 VALUES (?, ?, 'UNUSED', ?, ?, ?)`,
			seed.SeedText, result.EnrichedPrompt, result.TokensUsed, builder.Name(), metaJSON,
		); err != nil {
			return fmt.Errorf("insert seed #%d: %w", i+1, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit batch: %w", err)
	}

	slog.Info("batch build complete",
		"seeds_loaded", len(batch.Seeds),
		"builder", builder.Name(),
		"target_provider", batch.TargetProvider,
	)
	return nil
}
