/*
 * internal/publisher/composite.go
 *
 * Implements the publisher.Publisher interface by wrapping multiple child
 * publishers and executing them sequentially. Faults are isolated so that
 * a failure on one social platform does not block distribution to other active channels.
 */
package publisher

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

/* CompositePublisher distributes a video to multiple publishing adapters. */
type CompositePublisher struct {
	Publishers []Publisher
}

/* NewCompositePublisher constructs a CompositePublisher wrapping child publishers. */
func NewCompositePublisher(publishers ...Publisher) *CompositePublisher {
	return &CompositePublisher{
		Publishers: publishers,
	}
}

/* Name satisfies the publisher.Publisher interface. */
func (c *CompositePublisher) Name() string {
	var names []string
	for _, p := range c.Publishers {
		names = append(names, p.Name())
	}
	if len(names) == 0 {
		return "COMPOSITE (NONE)"
	}
	return fmt.Sprintf("COMPOSITE (%s)", strings.Join(names, ","))
}

/*
 * Publish uploads the video to all configured child platforms.
 * Fault isolation guarantees that if one platform fails, the others still succeed.
 * Returns an aggregated PlatformVideoID and URL string if at least one upload succeeds.
 */
func (c *CompositePublisher) Publish(ctx context.Context, req PublishRequest) (PublishResult, error) {
	if len(c.Publishers) == 0 {
		return PublishResult{}, fmt.Errorf("composite publisher has no active publishers configured")
	}

	var successfulIDs []string
	var successfulURLs []string
	var errors []string

	for _, p := range c.Publishers {
		slog.Info("composite dispatcher: invoking publisher", "target", p.Name())
		res, err := p.Publish(ctx, req)
		if err != nil {
			errStr := fmt.Sprintf("%s failed: %v", p.Name(), err)
			slog.Error("platform publishing error", "target", p.Name(), "error", err)
			errors = append(errors, errStr)
			continue
		}

		slog.Info("platform published successfully", "target", p.Name(), "video_id", res.PlatformVideoID, "url", res.URL)
		successfulIDs = append(successfulIDs, fmt.Sprintf("%s:%s", p.Name(), res.PlatformVideoID))
		successfulURLs = append(successfulURLs, res.URL)
	}

	// If all configured publishers failed, return a combined error
	if len(successfulIDs) == 0 {
		combinedErr := strings.Join(errors, "; ")
		return PublishResult{}, fmt.Errorf("all publication channels failed: %s", combinedErr)
	}

	// Warn if some platforms failed but proceed with successful ones
	if len(errors) > 0 {
		slog.Warn("some composite publishers failed during distribution", "errors", strings.Join(errors, "; "))
	}

	return PublishResult{
		PlatformVideoID: strings.Join(successfulIDs, ","),
		URL:             strings.Join(successfulURLs, " | "),
	}, nil
}
