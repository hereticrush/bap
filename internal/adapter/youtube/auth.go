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
 * clientSecretFile represents the JSON structure exported from the
 * Google Cloud Console for a Desktop-type OAuth 2.0 Client ID.
 */
type clientSecretFile struct {
	Installed struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		AuthURI      string `json:"auth_uri"`
		TokenURI     string `json:"token_uri"`
	} `json:"installed"`
}

/*
 * RunAuthFlow initiates an interactive CLI flow to obtain an OAuth token.
 * It reads the client secret, prints an authorization URL, waits for the
 * user to input the authorization code, and saves the resulting token.
 *
 * The redirect URI is hardcoded to the OOB (out-of-band) value for CLI
 * flows, which avoids the "missing redirect URL" error that occurs when
 * Google's exported JSON has an empty redirect_uris array.
 */
func RunAuthFlow(clientSecretPath, tokenPath string) error {
	b, err := os.ReadFile(clientSecretPath)
	if err != nil {
		return fmt.Errorf("unable to read client secret file: %w", err)
	}

	/* Parse the JSON to extract client credentials */
	var secret clientSecretFile
	if err := json.Unmarshal(b, &secret); err != nil {
		return fmt.Errorf("unable to parse client secret JSON: %w", err)
	}

	if secret.Installed.ClientID == "" || secret.Installed.ClientSecret == "" {
		return fmt.Errorf("client_secret.json is missing client_id or client_secret under the 'installed' key")
	}

	/*
	 * Build the OAuth2 config manually with an explicit redirect URI.
	 * "urn:ietf:wg:oauth:2.0:oob" tells Google to display the auth
	 * code on screen so the user can copy-paste it into the terminal.
	 */
	config := &oauth2.Config{
		ClientID:     secret.Installed.ClientID,
		ClientSecret: secret.Installed.ClientSecret,
		RedirectURL:  "urn:ietf:wg:oauth:2.0:oob",
		Scopes:       []string{youtube.YoutubeUploadScope},
		Endpoint:     google.Endpoint,
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

