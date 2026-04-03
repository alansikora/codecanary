package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// OAuthToken runs the OAuth PKCE flow and returns the access token.
// The caller provides all OAuth parameters so this function is provider-agnostic.
func OAuthToken(clientID, authorizeURL, tokenURL, scope string) (string, error) {
	// 1. Generate PKCE code verifier and challenge.
	verifier, err := generateCodeVerifier()
	if err != nil {
		return "", fmt.Errorf("generating code verifier: %w", err)
	}
	challenge := generateCodeChallenge(verifier)

	// 2. Start local callback server.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("starting callback server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	// 3. Generate state parameter.
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	// Channel to receive the auth code.
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			errCh <- fmt.Errorf("state mismatch")
			http.Error(w, "State mismatch", http.StatusBadRequest)
			return
		}
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			errCh <- fmt.Errorf("oauth error: %s — %s", errParam, r.URL.Query().Get("error_description"))
			_, _ = fmt.Fprintf(w, "<html><body><h2>Authentication failed</h2><p>%s</p><p>You can close this tab.</p></body></html>", r.URL.Query().Get("error_description"))
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			http.Error(w, "No code received", http.StatusBadRequest)
			return
		}
		codeCh <- code
		_, _ = fmt.Fprintf(w, "<html><body><h2>Authenticated!</h2><p>You can close this tab and return to the terminal.</p></body></html>")
	})

	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()) }()

	// 4. Build authorization URL and open browser.
	params := url.Values{
		"code":                  {"true"},
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"scope":                 {scope},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	authURL := authorizeURL + "?" + params.Encode()

	fmt.Printf("Opening browser to authenticate...\n")
	if err := OpenBrowser(authURL); err != nil {
		fmt.Printf("Open this URL in your browser:\n%s\n\n", authURL)
	}
	fmt.Printf("Waiting for authentication...\n")

	// 5. Wait for callback.
	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return "", err
	case <-time.After(5 * time.Minute):
		return "", fmt.Errorf("timed out waiting for authentication")
	}

	// 6. Exchange code for token.
	token, err := exchangeCode(code, verifier, redirectURI, state, clientID, tokenURL)
	if err != nil {
		return "", fmt.Errorf("exchanging code for token: %w", err)
	}

	return token, nil
}

func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func exchangeCode(code, verifier, redirectURI, state, clientID, tokenURL string) (string, error) {
	body := map[string]any{
		"grant_type":    "authorization_code",
		"client_id":     clientID,
		"code":          code,
		"code_verifier": verifier,
		"redirect_uri":  redirectURI,
		"state":         state,
		"expires_in":    31536000, // 1 year
	}
	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", fmt.Errorf("parsing token response: %w", err)
	}

	if resp.StatusCode != 200 {
		errJSON, _ := json.MarshalIndent(raw, "", "  ")
		return "", fmt.Errorf("token request failed (status %d):\n%s", resp.StatusCode, string(errJSON))
	}

	token, ok := raw["access_token"].(string)
	if !ok || token == "" {
		errJSON, _ := json.MarshalIndent(raw, "", "  ")
		return "", fmt.Errorf("no access_token in response:\n%s", string(errJSON))
	}

	return token, nil
}

// GitHubSecretExists checks whether a secret with the given name exists on a GitHub repo.
func GitHubSecretExists(repo, name string) bool {
	out, err := exec.Command("gh", "secret", "list", "--repo", repo).Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == name {
			return true
		}
	}
	return false
}

// SetGitHubSecret sets a secret on a GitHub repo using gh CLI.
func SetGitHubSecret(repo, name, value string) error {
	cmd := exec.Command("gh", "secret", "set", name, "--repo", repo)
	cmd.Stdin = strings.NewReader(value)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("setting secret: %w\n%s", err, string(out))
	}
	return nil
}

func OpenBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return fmt.Errorf("unsupported platform")
	}
}
