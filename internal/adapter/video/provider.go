/*
 * internal/adapter/video/provider.go
 *
 * Defines the AIVideoProvider interface and associated types.
 * Any AI video generation service (Runway, Luma, Kling) is integrated
 * by implementing this contract.
 */
package video

import "context"

/* GenerationStatus enumerates the possible states of an AI generation task. */
type GenerationStatus int

const (
	StatusPending    GenerationStatus = iota /* Task accepted, not yet started */
	StatusProcessing                        /* AI is generating the video */
	StatusCompleted                         /* Video is ready for download */
	StatusFailed                            /* Generation failed */
)

/*
 * GenerationRequest holds typed, provider-agnostic parameters
 * for a video generation call.
 */
type GenerationRequest struct {
	Prompt      string   /* The enriched prompt text */
	Duration    int      /* Target duration in seconds */
	Resolution  string   /* e.g., "1080p" */
	AspectRatio string   /* e.g., "16:9" */
	ImageURLs   []string /* Optional images used as generation anchors */
}

/*
 * GenerationResult holds the outcome of a status check
 * against the AI provider.
 */
type GenerationResult struct {
	Status   GenerationStatus
	VideoURL string /* Populated only when Status == StatusCompleted */
	Error    string /* Populated only when Status == StatusFailed */
}

/*
 * AIVideoProvider is the adapter interface.
 * Any AI video generation service can be integrated by implementing
 * this contract. Each adapter receives its provider-specific
 * configuration via dependency injection through its constructor.
 *
 * All methods accept a context.Context to support timeouts,
 * deadlines, and cancellation propagation from the Asynq worker.
 */
type AIVideoProvider interface {
	/* Name returns the provider identifier (e.g., "RUNWAY", "LUMA"). */
	Name() string

	/*
	 * GenerateVideo submits a video generation request to the AI provider.
	 * Returns the provider's task ID for subsequent status polling.
	 */
	GenerateVideo(ctx context.Context, req GenerationRequest) (taskID string, err error)

	/*
	 * CheckStatus polls the AI provider for the current state of a
	 * generation task identified by taskID.
	 */
	CheckStatus(ctx context.Context, taskID string) (GenerationResult, error)
}
