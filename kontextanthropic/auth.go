package kontextanthropic

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	defaultKontextLoginClientID = "app_a4fb6d20-e937-450f-aa19-db585405aa92"
	defaultKontextURL           = "https://api.kontext.security/mcp"
)

var defaultLoginScopes = []string{
	"openid",
	"email",
	"profile",
	"offline_access",
}

type authSession struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Subject      string
	Email        string
	Name         string
}

type oauthMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

type callbackResult struct {
	code  string
	state string
	err   error
}

func configWithDefaultAuth(ctx context.Context, cfg Config, out io.Writer) (Config, error) {
	if cfg.AccessToken != "" || cfg.UserID != "" {
		return cfg, nil
	}
	if os.Getenv("KONTEXT_LOCAL_SESSION") == "1" {
		cfg.UserID = "kontext-local-session"
		if cfg.APIBaseURL == "" {
			cfg.APIBaseURL = defaultAPIBaseURL()
		}
		return cfg, nil
	}
	if token := os.Getenv("KONTEXT_ACCESS_TOKEN"); token != "" {
		cfg.AccessToken = token
		cfg.UserID = getenv("KONTEXT_USER_ID", "kontext-env-user")
		if cfg.APIBaseURL == "" {
			cfg.APIBaseURL = defaultAPIBaseURL()
		}
		return cfg, nil
	}

	apiBaseURL := cfg.APIBaseURL
	if apiBaseURL == "" {
		apiBaseURL = defaultAPIBaseURL()
	}
	clientID := cfg.ClientID
	if clientID == "" {
		clientID = getenv("KONTEXT_LOGIN_CLIENT_ID", defaultKontextLoginClientID)
	}

	session, err := loginWithPKCE(ctx, apiBaseURL, clientID, out)
	if err != nil {
		return cfg, err
	}
	cfg.APIBaseURL = apiBaseURL
	cfg.AccessToken = session.AccessToken
	cfg.UserID = session.identityKey()
	return cfg, nil
}

func loginWithPKCE(ctx context.Context, apiBaseURL, clientID string, out io.Writer) (*authSession, error) {
	meta, err := discoverOAuth(ctx, apiBaseURL)
	if err != nil {
		return nil, err
	}

	callbackCh := make(chan callbackResult, 1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start callback server: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	server := &http.Server{Handler: callbackHandler("/callback", callbackCh)}
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Shutdown(context.Background())

	verifier := randomToken()
	challenge := pkceChallenge(verifier)
	state := randomToken()

	oauthConfig := &oauth2.Config{
		ClientID: clientID,
		Endpoint: oauth2.Endpoint{
			AuthURL:  meta.AuthorizationEndpoint,
			TokenURL: meta.TokenEndpoint,
		},
		RedirectURL: redirectURI,
		Scopes:      defaultLoginScopes,
	}

	authURL := oauthConfig.AuthCodeURL(
		state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	if out != nil {
		fmt.Fprintln(out, "Opening Kontext login...")
	}
	if shouldOpenBrowser() {
		if err := openBrowser(authURL); err != nil && out != nil {
			fmt.Fprintf(out, "Browser open failed. Open this URL manually:\n%s\n", authURL)
		}
	} else if out != nil {
		fmt.Fprintf(out, "Open this URL manually:\n%s\n", authURL)
	}

	var callback callbackResult
	select {
	case callback = <-callbackCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if callback.err != nil {
		return nil, callback.err
	}
	if callback.state != state {
		return nil, fmt.Errorf("oauth state mismatch")
	}

	token, err := oauthConfig.Exchange(ctx, callback.code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}

	session := &authSession{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry,
	}
	if rawIDToken, ok := token.Extra("id_token").(string); ok {
		if err := applyIDToken(session, rawIDToken); err != nil {
			return nil, err
		}
	}
	if session.Subject == "" {
		return nil, fmt.Errorf("id token missing subject claim")
	}
	return session, nil
}

func discoverOAuth(ctx context.Context, apiBaseURL string) (*oauthMetadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(apiBaseURL, "/")+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth discovery request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth discovery failed: %s", resp.Status)
	}

	var meta oauthMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("decode oauth discovery: %w", err)
	}
	return &meta, nil
}

func callbackHandler(path string, ch chan<- callbackResult) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != path {
			http.NotFound(w, req)
			return
		}
		if errMsg := req.URL.Query().Get("error"); errMsg != "" {
			ch <- callbackResult{err: fmt.Errorf("oauth error: %s: %s", errMsg, req.URL.Query().Get("error_description"))}
			http.Error(w, "OAuth failed", http.StatusBadRequest)
			return
		}
		code := req.URL.Query().Get("code")
		if code == "" {
			ch <- callbackResult{err: fmt.Errorf("no authorization code received")}
			http.Error(w, "Missing code", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, "Kontext Go SDK connected. You can return to the terminal.")
		ch <- callbackResult{code: code, state: req.URL.Query().Get("state")}
	})
}

func applyIDToken(session *authSession, rawIDToken string) error {
	parts := strings.Split(rawIDToken, ".")
	if len(parts) != 3 {
		return fmt.Errorf("invalid id token")
	}
	payload := parts[1]
	if remainder := len(payload) % 4; remainder != 0 {
		payload += strings.Repeat("=", 4-remainder)
	}
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return err
	}
	var claims struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return err
	}
	session.Subject = claims.Subject
	session.Email = claims.Email
	session.Name = claims.Name
	return nil
}

func (s *authSession) identityKey() string {
	switch {
	case s.Subject != "":
		return s.Subject
	case s.Email != "":
		return s.Email
	default:
		return "kontext-user"
	}
}

func defaultAPIBaseURL() string {
	return apiBaseURLFromKontextURL(getenv("KONTEXT_URL", defaultKontextURL))
}

func apiBaseURLFromKontextURL(kontextURL string) string {
	return strings.TrimSuffix(strings.TrimRight(kontextURL, "/"), "/mcp")
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func shouldOpenBrowser() bool {
	return os.Getenv("KONTEXT_OPEN_BROWSER") != "0" && os.Getenv("KONTEXT_DEMO_OPEN_BROWSER") != "0"
}

func openBrowser(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "linux":
		return exec.Command("xdg-open", target).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
}

func randomToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
