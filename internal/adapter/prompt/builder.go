/*
 * internal/adapter/prompt/builder.go
 *
 * Defines the AIPromptBuilder interface and associated types.
 * Any LLM service (Gemini, Kimi, GPT) is integrated by implementing
 * this contract. Also defines the BatchInput and SeedEntry types
 * used by the build-prompts CLI command.
 */
package prompt

import "context"

/*
 * PromptBuildRequest contains the input for a single prompt
 * enrichment call. The builder receives a seed and expands it
 * into a production-ready video generation prompt.
 */
type PromptBuildRequest struct {
	SystemPrompt   string            /* The system-level instruction for the LLM */
	SeedPrompt     string            /* The short seed idea to expand */
	TargetProvider string            /* Which video AI to format for (e.g., "RUNWAY") */
	Metadata       map[string]string /* Optional per-seed style hints (e.g., "mood": "ethereal") */
}

/*
 * PromptBuildResult contains the output of a single prompt
 * enrichment call.
 */
type PromptBuildResult struct {
	EnrichedPrompt string /* The expanded, production-ready prompt */
	TokensUsed     int    /* Number of tokens consumed by the LLM */
}

/*
 * AIPromptBuilder is the adapter interface for LLM-based prompt
 * enrichment. Each implementation wraps a specific LLM service.
 *
 * The PassthroughAdapter is a special case that returns the seed
 * text as-is, useful for testing or fully handcrafted prompts.
 */
type AIPromptBuilder interface {
	/* Name returns the builder identifier (e.g., "GEMINI", "PASSTHROUGH"). */
	Name() string

	/*
	 * BuildPrompt enriches a single seed prompt using the LLM.
	 * Returns the enriched result or an error (which triggers
	 * a full batch rollback in the build-prompts command).
	 */
	BuildPrompt(ctx context.Context, req PromptBuildRequest) (PromptBuildResult, error)
}

/*
 * BatchInput is the top-level structure parsed from the weekly
 * seeds JSON file provided to the build-prompts CLI command.
 *
 * Example JSON:
 *   {
 *     "system_prompt": "You are a cinematic video prompt engineer...",
 *     "target_provider": "RUNWAY",
 *     "seeds": [
 *       { "seed_text": "underwater city with bioluminescent buildings",
 *         "metadata": { "style": "sci-fi" } }
 *     ]
 *   }
 */
type BatchInput struct {
	SystemPrompt   string      `json:"system_prompt"`
	TargetProvider string      `json:"target_provider"`
	Seeds          []SeedEntry `json:"seeds"`
}

/*
 * SeedEntry represents a single seed idea within the batch
 * input JSON file.
 */
type SeedEntry struct {
	SeedText string            `json:"seed_text"`
	Metadata map[string]string `json:"metadata"`
}
