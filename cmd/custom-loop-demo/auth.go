package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

var defaultLoginScopes = []string{
	"openid",
	"email",
	"profile",
	"offline_access",
}

var identityLoginScopes = []string{
	"openid",
	"email",
	"profile",
}

type oauthMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

type loginSession struct {
	IssuerURL    string
	AccessToken  string
	IDToken      string
	RefreshToken string
	ExpiresAt    time.Time
	Subject      string
	Email        string
	Name         string
}

type tokenExchangeResponse struct {
	AccessToken string `json:"access_token"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

func loginWithPKCE(ctx context.Context, cfg demoConnectConfig, out io.Writer, clientID string, scopes ...string) (*loginSession, error) {
	meta, err := discoverOAuth(ctx, cfg.apiBaseURL)
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
		Scopes:      resolveLoginScopes(scopes),
	}

	authURL := oauthConfig.AuthCodeURL(
		state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	fmt.Fprintf(out, "  Opening PKCE login for %s...\n", clientID)
	if shouldOpenBrowser() {
		if err := openBrowser(authURL); err != nil {
			fmt.Fprintf(out, "  Browser open failed: %v\n", err)
			fmt.Fprintf(out, "  Open this URL manually:\n  %s\n", authURL)
		} else if shouldShowAuthURL() {
			fmt.Fprintf(out, "  Auth URL:\n  %s\n", authURL)
		}
	} else {
		fmt.Fprintln(out, "  Browser open skipped because KONTEXT_DEMO_OPEN_BROWSER=0.")
		fmt.Fprintf(out, "  Open this URL manually:\n  %s\n", authURL)
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

	session := &loginSession{
		IssuerURL:    cfg.apiBaseURL,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry,
	}
	if rawIDToken, ok := token.Extra("id_token").(string); ok {
		session.IDToken = rawIDToken
		if err := applyIDToken(session, rawIDToken); err != nil {
			return nil, err
		}
	}
	if includesScope(oauthConfig.Scopes, "openid") && session.Subject == "" {
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

func exchangeGatewayToken(ctx context.Context, cfg demoConnectConfig, session *loginSession, clientID string) (string, error) {
	meta, err := discoverOAuth(ctx, cfg.apiBaseURL)
	if err != nil {
		return "", err
	}

	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"client_id":          {clientID},
		"subject_token":      {session.AccessToken},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"resource":           {"mcp-gateway"},
		"scope":              {"gateway:access"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+session.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gateway token exchange: %w", err)
	}
	defer resp.Body.Close()

	var payload tokenExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode gateway token exchange: %w", err)
	}
	if payload.Error != "" {
		return "", fmt.Errorf("gateway token exchange failed: %s: %s", payload.Error, payload.ErrorDesc)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("gateway token exchange returned empty access_token")
	}
	return payload.AccessToken, nil
}

type callbackResult struct {
	code  string
	state string
	err   error
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
		fmt.Fprintln(w, "Kontext Go SDK demo connected. You can return to the terminal.")
		ch <- callbackResult{code: code, state: req.URL.Query().Get("state")}
	})
}

func applyIDToken(session *loginSession, rawIDToken string) error {
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

func (s *loginSession) identityKey() string {
	return strings.TrimRight(s.IssuerURL, "/") + "#" + s.Subject
}

func (s *loginSession) displayIdentity() string {
	if s.Email != "" {
		return s.Email
	}
	if s.Name != "" {
		return s.Name
	}
	return s.Subject
}

func resolveLoginScopes(scopes []string) []string {
	baseScopes := defaultLoginScopes
	if len(scopes) > 0 {
		baseScopes = identityLoginScopes
	}

	resolved := append([]string(nil), baseScopes...)
	for _, scope := range scopes {
		if scope != "" && !includesScope(resolved, scope) {
			resolved = append(resolved, scope)
		}
	}
	return resolved
}

func includesScope(scopes []string, target string) bool {
	for _, scope := range scopes {
		if scope == target {
			return true
		}
	}
	return false
}
