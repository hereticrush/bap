/*
 * internal/adapter/youtube/auth.go
 *
 * Implements the OAuth2 interactive web flow for generating a token.json
 * file from a client_secret.json file.
 */
package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/youtube/v3"
)

/*
 * RunAuthFlow initiates an interactive CLI flow to obtain an OAuth token.
 * It reads the client secret, prints an authorization URL, waits for the
 * user to input the authorization code, and saves the resulting token.
 */
func RunAuthFlow(clientSecretPath, tokenPath string) error {
	b, err := os.ReadFile(clientSecretPath)
	if err != nil {
		return fmt.Errorf("unable to read client secret file: %w", err)
	}

	// Request YoutubeUploadScope to allow uploading videos
	config, err := google.ConfigFromJSON(b, youtube.YoutubeUploadScope)
	if err != nil {
		return fmt.Errorf("unable to parse client secret file to config: %w", err)
	}

	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the authorization code: \n%v\n", authURL)

	fmt.Print("Enter authorization code: ")
	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		return fmt.Errorf("unable to read authorization code: %w", err)
	}

	tok, err := config.Exchange(context.TODO(), authCode)
	if err != nil {
		return fmt.Errorf("unable to retrieve token from web: %w", err)
	}

	return saveToken(tokenPath, tok)
}

/*
 * saveToken saves a token to a file path.
 */
func saveToken(path string, token *oauth2.Token) error {
	fmt.Printf("Saving credential file to: %s\n", path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("unable to cache oauth token: %w", err)
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(token); err != nil {
		return fmt.Errorf("unable to encode oauth token: %w", err)
	}

	return nil
}
