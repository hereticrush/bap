/*
 * internal/adapter/youtube/auth.go
 *
 * Implements the OAuth2 interactive web flow for generating a token.json
 * file from a client_secret.json file.
 *
 * Uses a localhost loopback redirect (http://localhost:8085) to capture
 * the authorization code automatically — no manual copy-paste needed.
 */
package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/youtube/v3"
)

/* Port used for the temporary OAuth callback server */
const oauthCallbackPort = "8085"

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
 *
 * Flow:
 *   1. Parse client_secret.json to extract credentials.
 *   2. Start a temporary HTTP server on localhost:8085.
 *   3. Print the Google consent URL for the user to open.
 *   4. Wait for Google to redirect back with the authorization code.
 *   5. Exchange the code for access + refresh tokens.
 *   6. Save the token to disk and shut down the server.
 *
 * Prerequisites:
 *   - For Web-type OAuth clients, http://localhost:8085 must be
 *     registered as an Authorized Redirect URI in Google Cloud Console.
 *   - For Desktop-type (installed) clients, localhost redirects are
 *     automatically allowed by Google.
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
	 * Build the OAuth2 config with a localhost loopback redirect.
	 * This replaces the deprecated urn:ietf:wg:oauth:2.0:oob flow.
	 */
	redirectURL := "http://localhost:" + oauthCallbackPort
	config := &oauth2.Config{
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{youtube.YoutubeUploadScope},
		Endpoint:     google.Endpoint,
	}

	/* Channels to communicate between the callback server and main flow */
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	/* Start a temporary HTTP server to receive the OAuth callback */
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		/* Ignore favicon.ico and other browser noise */
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "No authorization code received.", http.StatusBadRequest)
			errCh <- fmt.Errorf("callback received but no authorization code was present")
			return
		}
		fmt.Fprintln(w, "Authorization successful! You can close this tab and return to the terminal.")
		codeCh <- code
	})

	srv := &http.Server{Addr: ":" + oauthCallbackPort, Handler: mux}
	go func() {
		if srvErr := srv.ListenAndServe(); srvErr != nil && srvErr != http.ErrServerClosed {
			errCh <- fmt.Errorf("callback server failed to start on port %s: %w", oauthCallbackPort, srvErr)
		}
	}()

	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("\nOpen this URL in your browser to authorize:\n\n  %s\n\n", authURL)
	fmt.Printf("Listening for callback on %s ...\n", redirectURL)

	/* Block until we receive the code or an error */
	var authCode string
	select {
	case authCode = <-codeCh:
		/* success — code received */
	case cbErr := <-errCh:
		_ = srv.Shutdown(context.Background())
		return cbErr
	}

	/* Shut down the callback server immediately */
	_ = srv.Shutdown(context.Background())

	tok, err := config.Exchange(context.TODO(), authCode)
	if err != nil {
		return fmt.Errorf("unable to exchange authorization code for token: %w", err)
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


