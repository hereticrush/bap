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
 * clientCredentials holds the fields we need from a Google OAuth JSON file.
 */
type clientCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

/*
 * clientSecretFile represents the JSON structure exported from the
 * Google Cloud Console. Supports both Desktop ("installed") and
 * Web Application ("web") OAuth 2.0 Client ID types.
 */
type clientSecretFile struct {
	Installed *clientCredentials `json:"installed"`
	Web       *clientCredentials `json:"web"`
}

/*
 * credentials returns whichever credential block is present,
 * preferring "installed" (Desktop) over "web".
 */
func (c *clientSecretFile) credentials() *clientCredentials {
	if c.Installed != nil && c.Installed.ClientID != "" {
		return c.Installed
	}
	if c.Web != nil && c.Web.ClientID != "" {
		return c.Web
	}
	return nil
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

	creds := secret.credentials()
	if creds == nil {
		return fmt.Errorf("client_secret.json must contain an 'installed' or 'web' key with client_id and client_secret")
	}

	/*
	 * Build the OAuth2 config manually with an explicit redirect URI.
	 * "urn:ietf:wg:oauth:2.0:oob" tells Google to display the auth
	 * code on screen so the user can copy-paste it into the terminal.
	 */
	config := &oauth2.Config{
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
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

