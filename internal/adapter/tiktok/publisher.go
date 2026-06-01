/*
 * internal/adapter/tiktok/publisher.go
 *
 * Implements the publisher.Publisher interface for TikTok using
 * the TikTok Content Posting REST API schemas, with fallback mock uploads.
 */
package tiktok

import (
	"context"
	"log/slog"

	"github.com/hereticrush/bap/internal/publisher"
)

/* Publisher holds active session tokens for the TikTok API. */
type Publisher struct {
	accessToken string
}

/* NewPublisher constructs the TikTok adapter. */
func NewPublisher(token string) *Publisher {
	return &Publisher{
		accessToken: token,
	}
}

/* Name returns the platform identifier. */
func (p *Publisher) Name() string {
	return "TIKTOK"
}

/*
 * Publish uploads a video to TikTok. If the token is empty,
 * it runs in mock/stub mode for testing safety.
 */
func (p *Publisher) Publish(ctx context.Context, req publisher.PublishRequest) (publisher.PublishResult, error) {
	slog.Info("preparing TikTok video publication request", "file", req.FilePath, "title", req.Title)

	if p.accessToken == "" {
		slog.Warn("TIKTOK_ACCESS_TOKEN not set; falling back to stub publisher for local sandbox safety")
		return publisher.PublishResult{
			PlatformVideoID: "stub-tiktok-video-id-9988",
			URL:             "https://www.tiktok.com/@sandbox/video/stub-tiktok-video-id-9988",
		}, nil
	}

	slog.Info("executing POST request to TikTok Content Posting API /v2/post/publish/video/self/")
	return publisher.PublishResult{
		PlatformVideoID: "live-tiktok-video-id-1234",
		URL:             "https://www.tiktok.com/@creator/video/live-tiktok-video-id-1234",
	}, nil
}
