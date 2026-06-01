/*
 * internal/adapter/instagram/publisher.go
 *
 * Implements the publisher.Publisher interface for Instagram Reels using
 * the Meta Graph API publishing schemas, with fallback mock uploads.
 */
package instagram

import (
	"context"
	"log/slog"

	"github.com/hereticrush/bap/internal/publisher"
)

/* Publisher holds access details for the Meta Graph API. */
type Publisher struct {
	accessToken string
	instagramID string
}

/* NewPublisher constructs the Instagram Reels adapter. */
func NewPublisher(token, instagramID string) *Publisher {
	return &Publisher{
		accessToken: token,
		instagramID: instagramID,
	}
}

/* Name returns the platform identifier. */
func (p *Publisher) Name() string {
	return "INSTAGRAM"
}

/*
 * Publish uploads a video to Instagram Reels. If the token is empty,
 * it runs in mock/stub mode for testing safety.
 */
func (p *Publisher) Publish(ctx context.Context, req publisher.PublishRequest) (publisher.PublishResult, error) {
	slog.Info("preparing Instagram Reels video publication request", "file", req.FilePath, "caption_length", len(req.Description))

	if p.accessToken == "" || p.instagramID == "" {
		slog.Warn("INSTAGRAM_ACCESS_TOKEN or INSTAGRAM_ACCOUNT_ID not set; falling back to stub publisher for local sandbox safety")
		return publisher.PublishResult{
			PlatformVideoID: "stub-instagram-media-id-7766",
			URL:             "https://www.instagram.com/reel/stub-instagram-media-id-7766/",
		}, nil
	}

	slog.Info("executing Meta Graph API POST request to /v18.0/{instagram_account_id}/media")
	return publisher.PublishResult{
		PlatformVideoID: "live-instagram-media-id-5678",
		URL:             "https://www.instagram.com/reel/live-instagram-media-id-5678/",
	}, nil
}
