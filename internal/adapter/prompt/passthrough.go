/*
 * internal/adapter/prompt/passthrough.go
 *
 * PassthroughAdapter implements AIPromptBuilder by returning the
 * seed text as-is without any LLM enrichment. Useful for:
 *   - Testing the batch pipeline without LLM costs
 *   - Loading fully handcrafted prompts that need no expansion
 */
package prompt

import "context"

/* PassthroughAdapter returns seed prompts unchanged. */
type PassthroughAdapter struct{}

/* NewPassthroughAdapter creates a new PassthroughAdapter. */
func NewPassthroughAdapter() *PassthroughAdapter {
	return &PassthroughAdapter{}
}

/* Name returns the adapter identifier. */
func (p *PassthroughAdapter) Name() string {
	return "PASSTHROUGH"
}

/*
 * BuildPrompt returns the seed prompt as-is.
 * TokensUsed is always 0 since no LLM is invoked.
 */
func (p *PassthroughAdapter) BuildPrompt(ctx context.Context, req PromptBuildRequest) (PromptBuildResult, error) {
	return PromptBuildResult{
		EnrichedPrompt: req.SeedPrompt,
		TokensUsed:     0,
	}, nil
}
