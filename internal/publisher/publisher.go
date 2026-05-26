/*
 * internal/publisher/publisher.go
 *
 * Defines the generic interface for video publishing platforms.
 */
package publisher

import "context"

/*
 * PublishRequest holds platform-agnostic metadata required to upload a video.
 */
type PublishRequest struct {
	FilePath      string   /* Path to the local video file */
	Title         string   /* Video title */
	Description   string   /* Video description */
	Tags          []string /* Array of tags */
	Privacy       string   /* e.g., "private", "public", "unlisted" */
	ThumbnailPath string   /* Optional local image path for custom thumbnail */
	PlaylistID    string   /* Optional target playlist identifier */
}

/*
 * PublishResult is the outcome of a successful publish operation.
 */
type PublishResult struct {
	PlatformVideoID string /* e.g., YouTube Video ID, TikTok Item ID */
	URL             string /* Public or private link to the video */
}

/*
 * Publisher is the adapter interface.
 * Any platform (YouTube, TikTok, Instagram) can be integrated by implementing
 * this contract.
 */
type Publisher interface {
	/* Name returns the platform identifier (e.g., "YOUTUBE", "TIKTOK"). */
	Name() string

	/*
	 * Publish uploads the video and metadata to the target platform.
	 * Returns the resulting ID and URL.
	 */
	Publish(ctx context.Context, req PublishRequest) (PublishResult, error)
}
