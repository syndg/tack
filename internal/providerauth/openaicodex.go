// Package providerauth contains Tack-owned provider authentication flows.
//
// The OpenAI Codex OAuth flow in this file is closely inspired by Pi's
// MIT-licensed implementation in:
// https://github.com/badlogic/pi-mono
//
// Attribution: Mario Zechner and contributors, MIT License.
package providerauth

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

const (
	openAICodexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAICodexAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	openAICodexTokenURL     = "https://auth.openai.com/oauth/token"
	openAICodexRedirectURI  = "http://localhost:1455/auth/callback"
	openAICodexScope        = "openid profile email offline_access"
	openAICodexJWTClaimPath = "https://api.openai.com/auth"
)

type OAuthCredential struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
	AccountID    string
}

type OAuthLoginCallbacks struct {
	OnAuth   func(url, instructions string)
	OnPrompt func(message string) (string, error)
}

func LoginOpenAICodex(ctx context.Context, callbacks OAuthLoginCallbacks) (*OAuthCredential, error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, err
	}
	state, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	authURL := buildOpenAICodexURL(challenge, state)
	server, err := startOpenAICodexCallbackServer(state)
	if err != nil {
		return nil, err
	}
	defer server.close()
	if callbacks.OnAuth != nil {
		callbacks.OnAuth(authURL, "A browser window should open. If it does not, open the URL manually. If callback auth fails, paste the full redirect URL when prompted.")
	}
	openBrowser(authURL)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		code, err := server.waitForCode(ctx)
		if err != nil {
			errCh <- err
			return
		}
		codeCh <- code
	}()

	var code string
	select {
	case <-time.After(90 * time.Second):
		if callbacks.OnPrompt == nil {
			return nil, fmt.Errorf("oauth callback timed out")
		}
		input, err := callbacks.OnPrompt("Paste the full redirect URL or authorization code")
		if err != nil {
			return nil, err
		}
		parsedCode, parsedState := parseAuthorizationInput(input)
		if parsedState != "" && parsedState != state {
			return nil, fmt.Errorf("state mismatch")
		}
		if parsedCode == "" {
			return nil, fmt.Errorf("missing authorization code")
		}
		code = parsedCode
	case err := <-errCh:
		if callbacks.OnPrompt == nil {
			return nil, err
		}
		input, promptErr := callbacks.OnPrompt("Paste the full redirect URL or authorization code")
		if promptErr != nil {
			return nil, err
		}
		parsedCode, parsedState := parseAuthorizationInput(input)
		if parsedState != "" && parsedState != state {
			return nil, fmt.Errorf("state mismatch")
		}
		if parsedCode == "" {
			return nil, err
		}
		code = parsedCode
	case code = <-codeCh:
	}

	token, err := exchangeOpenAICodexCode(ctx, code, verifier)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func RefreshOpenAICodexToken(ctx context.Context, refreshToken string) (*OAuthCredential, error) {
	body := url.Values{}
	body.Set("grant_type", "refresh_token")
	body.Set("refresh_token", refreshToken)
	body.Set("client_id", openAICodexClientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAICodexTokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("OpenAI Codex token refresh failed: %s", resp.Status)
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	accountID, err := extractOpenAICodexAccountID(payload.AccessToken)
	if err != nil {
		return nil, err
	}
	return &OAuthCredential{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second).UnixMilli(),
		AccountID:    accountID,
	}, nil
}

func buildOpenAICodexURL(challenge, state string) string {
	u, _ := url.Parse(openAICodexAuthorizeURL)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", openAICodexClientID)
	q.Set("redirect_uri", openAICodexRedirectURI)
	q.Set("scope", openAICodexScope)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("originator", "tack")
	u.RawQuery = q.Encode()
	return u.String()
}

type openAICodexCallbackServer struct {
	server *http.Server
	codeCh chan string
	errCh  chan error
}

func startOpenAICodexCallbackServer(state string) (*openAICodexCallbackServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return nil, err
	}
	srv := &openAICodexCallbackServer{codeCh: make(chan string, 1), errCh: make(chan error, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("State mismatch"))
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Missing authorization code"))
			return
		}
		_, _ = w.Write([]byte("Authentication successful. Return to Tack."))
		select {
		case srv.codeCh <- code:
		default:
		}
	})
	srv.server = &http.Server{Handler: mux}
	go func() {
		if err := srv.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			select {
			case srv.errCh <- err:
			default:
			}
		}
	}()
	return srv, nil
}

func (s *openAICodexCallbackServer) waitForCode(ctx context.Context) (string, error) {
	select {
	case code := <-s.codeCh:
		return code, nil
	case err := <-s.errCh:
		return "", err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (s *openAICodexCallbackServer) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.server.Shutdown(ctx)
}

func exchangeOpenAICodexCode(ctx context.Context, code, verifier string) (*OAuthCredential, error) {
	body := url.Values{}
	body.Set("grant_type", "authorization_code")
	body.Set("client_id", openAICodexClientID)
	body.Set("code", code)
	body.Set("code_verifier", verifier)
	body.Set("redirect_uri", openAICodexRedirectURI)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAICodexTokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("OpenAI Codex token exchange failed: %s", resp.Status)
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	accountID, err := extractOpenAICodexAccountID(payload.AccessToken)
	if err != nil {
		return nil, err
	}
	return &OAuthCredential{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second).UnixMilli(),
		AccountID:    accountID,
	}, nil
}

func parseAuthorizationInput(input string) (string, string) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", ""
	}
	if parsed, err := url.Parse(value); err == nil {
		return parsed.Query().Get("code"), parsed.Query().Get("state")
	}
	if strings.Contains(value, "#") {
		parts := strings.SplitN(value, "#", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
	}
	if strings.Contains(value, "code=") {
		params, err := url.ParseQuery(value)
		if err == nil {
			return params.Get("code"), params.Get("state")
		}
	}
	return value, ""
}

func extractOpenAICodexAccountID(accessToken string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid OpenAI Codex access token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		return "", err
	}
	claim, _ := data[openAICodexJWTClaimPath].(map[string]any)
	accountID, _ := claim["chatgpt_account_id"].(string)
	if accountID == "" {
		return "", fmt.Errorf("failed to extract account id from OpenAI Codex access token")
	}
	return accountID, nil
}

func generatePKCE() (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier := base64.RawURLEncoding.EncodeToString(buf)
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	return verifier, challenge, nil
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}

func openBrowser(target string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	_ = cmd.Start()
}
