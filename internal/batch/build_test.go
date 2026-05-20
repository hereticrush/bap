package batch

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hereticrush/bap/internal/adapter/prompt"
	"github.com/hereticrush/bap/internal/db"
	_ "github.com/mattn/go-sqlite3"
)

type stubBuilder struct{}

func (stubBuilder) Name() string { return "PASSTHROUGH" }
func (stubBuilder) BuildPrompt(ctx context.Context, req prompt.PromptBuildRequest) (prompt.PromptBuildResult, error) {
	return prompt.PromptBuildResult{EnrichedPrompt: req.SeedPrompt, TokensUsed: 0}, nil
}

func TestBuildBatchPersistsMetadata(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	input := prompt.BatchInput{
		SystemPrompt:   "test system",
		TargetProvider: "RUNWAY",
		Seeds: []prompt.SeedEntry{
			{
				SeedText: "seed one",
				Metadata: map[string]string{"voice_script": "Say one"},
			},
			{SeedText: "seed two"},
		},
	}

	if err := BuildBatch(context.Background(), database, stubBuilder{}, input); err != nil {
		t.Fatalf("BuildBatch: %v", err)
	}

	var count int
	database.QueryRow("SELECT COUNT(*) FROM prompts WHERE status = 'UNUSED'").Scan(&count)
	if count != 2 {
		t.Fatalf("expected 2 prompts, got %d", count)
	}

	var meta sql.NullString
	err = database.QueryRow(
		"SELECT metadata FROM prompts WHERE seed_text = 'seed one'",
	).Scan(&meta)
	if err != nil {
		t.Fatalf("query metadata: %v", err)
	}
	if !meta.Valid || meta.String != `{"voice_script":"Say one"}` {
		t.Errorf("metadata = %v", meta)
	}
}
