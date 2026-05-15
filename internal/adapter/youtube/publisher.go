/*
 * internal/adapter/youtube/publisher.go
 *
 * Implements the publisher.Publisher interface for YouTube using the
 * Google OAuth2 and YouTube Data APIs.
 */
package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hereticrush/bap/internal/publisher"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

/*
 * Publisher holds the necessary paths for OAuth credentials.
 */
type Publisher struct {
	clientSecretPath string
	tokenPath        string
}

/*
 * NewPublisher creates a new YouTube publisher adapter.
 * It expects the credentials to be at:
 *   - credentials/youtube/client_secret.json
 *   - credentials/youtube/token.json
 */
func NewPublisher(clientSecretPath, tokenPath string) *Publisher {
	return &Publisher{
		clientSecretPath: clientSecretPath,
		tokenPath:        tokenPath,
	}
}

/* Name satisfies the publisher.Publisher interface. */
func (p *Publisher) Name() string {
	return "YOUTUBE"
}

/*
 * Publish uploads a video to YouTube using the provided metadata.
 * Requires valid OAuth2 tokens.
 */
func (p *Publisher) Publish(ctx context.Context, req publisher.PublishRequest) (publisher.PublishResult, error) {
	client, err := p.getClient(ctx)
	if err != nil {
		return publisher.PublishResult{}, fmt.Errorf("oauth client: %w", err)
	}

	service, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return publisher.PublishResult{}, fmt.Errorf("youtube service: %w", err)
	}

	uploadFile, err := os.Open(req.FilePath)
	if err != nil {
		return publisher.PublishResult{}, fmt.Errorf("open video file: %w", err)
	}
	defer uploadFile.Close()

	privacy := req.Privacy
	if privacy == "" {
		privacy = "private" /* Default to private for safety */
	}

	video := &youtube.Video{
		Snippet: &youtube.VideoSnippet{
			Title:       req.Title,
			Description: req.Description,
			Tags:        req.Tags,
		},
		Status: &youtube.VideoStatus{
			PrivacyStatus: privacy,
		},
	}

	call := service.Videos.Insert([]string{"snippet", "status"}, video)
	call = call.Media(uploadFile)

	response, err := call.Do()
	if err != nil {
		return publisher.PublishResult{}, fmt.Errorf("insert video: %w", err)
	}

	return publisher.PublishResult{
		PlatformVideoID: response.Id,
		URL:             fmt.Sprintf("https://www.youtube.com/watch?v=%s", response.Id),
	}, nil
}

/*
 * getClient builds an HTTP client from the client_secret and token files.
 */
func (p *Publisher) getClient(ctx context.Context) (*http.Client, error) {
	b, err := os.ReadFile(p.clientSecretPath)
	if err != nil {
		return nil, fmt.Errorf("read client secret (%s): %w", p.clientSecretPath, err)
	}

	config, err := google.ConfigFromJSON(b, youtube.YoutubeUploadScope)
	if err != nil {
		return nil, fmt.Errorf("parse client secret: %w", err)
	}

	tokenFile, err := os.Open(p.tokenPath)
	if err != nil {
		return nil, fmt.Errorf("open token file (%s): %w", p.tokenPath, err)
	}
	defer tokenFile.Close()

	tok := &oauth2.Token{}
	if err := json.NewDecoder(tokenFile).Decode(tok); err != nil {
		return nil, fmt.Errorf("decode token JSON: %w", err)
	}

	return config.Client(ctx, tok), nil
}
