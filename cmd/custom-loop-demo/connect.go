package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const (
	defaultKontextLoginClientID = "app_a4fb6d20-e937-450f-aa19-db585405aa92"
	defaultKontextURL           = "https://api.kontext.security/mcp"
)

type demoIdentity struct {
	Email       string
	Org         string
	IssuerURL   string
	UserID      string
	AccessToken string
	APIBaseURL  string
	session     *loginSession
}

func runKontextLoginDemo(ctx context.Context, out io.Writer) (demoIdentity, demoConnectConfig, error) {
	fmt.Fprintln(out, "Step 1: Login with Kontext PKCE")

	cfg, err := demoConnectConfigFromEnv()
	if err != nil {
		return demoIdentity{}, demoConnectConfig{}, err
	}

	fmt.Fprintf(out, "  Login client: %s\n", cfg.loginClientID)
	if cfg.externalClientID != "" {
		fmt.Fprintf(out, "  External app client available: %s (not used for CLI-style login)\n", cfg.externalClientID)
	}
	fmt.Fprintf(out, "  Kontext API URL: %s\n", cfg.apiBaseURL)
	fmt.Fprintln(out, "  PKCE callback: native localhost callback on a free port, same as kontext-cli")

	session, err := loginWithPKCE(ctx, cfg, out, cfg.loginClientID)
	if err != nil {
		return demoIdentity{}, demoConnectConfig{}, err
	}
	fmt.Fprintf(out, "  Authenticated as %s.\n", session.displayIdentity())

	identity := demoIdentity{
		Email:       session.Email,
		Org:         "Kontext organization",
		IssuerURL:   cfg.apiBaseURL,
		UserID:      session.identityKey(),
		AccessToken: session.AccessToken,
		APIBaseURL:  cfg.apiBaseURL,
		session:     session,
	}
	fmt.Fprintln(out)
	return identity, cfg, nil
}

func localDemoIdentity() (demoIdentity, demoConnectConfig) {
	cfg, _ := demoConnectConfigFromEnv()
	return demoIdentity{
		Email:      "local-demo@kontext.security",
		Org:        "Local demo",
		IssuerURL:  cfg.apiBaseURL,
		UserID:     "local-demo",
		APIBaseURL: cfg.apiBaseURL,
	}, cfg
}

func runHostedConnectDemo(ctx context.Context, ui *demoUI, identity demoIdentity, cfg demoConnectConfig, managedClientID string) error {
	out := ui.DebugWriter()
	fmt.Fprintln(out, "Step 3: Open hosted provider integrations")
	ui.Section("Connect")

	credentialClientID := managedClientID
	source := "managed agent client returned by CreateSession"
	if cfg.useExternalClient {
		if cfg.externalClientID == "" {
			return fmt.Errorf("KONTEXT_DEMO_CONNECT_CLIENT=external requires KONTEXT_CLIENT_ID")
		}
		credentialClientID = cfg.externalClientID
		source = "KONTEXT_CLIENT_ID external override"
	}
	if credentialClientID == "" {
		return fmt.Errorf("missing credential client id for hosted connect")
	}
	fmt.Fprintf(out, "  Connect client: %s (%s)\n", credentialClientID, source)

	gatewayToken := cfg.gatewayToken
	if gatewayToken == "" {
		var err error
		gatewayToken, err = exchangeGatewayToken(ctx, cfg, identity.session, credentialClientID)
		if needsGatewayAccessReauthentication(err) {
			fmt.Fprintln(out, "  Session needs gateway access. Opening browser to authorize the managed agent client...")
			gatewaySession, loginErr := loginWithPKCE(ctx, cfg, out, credentialClientID, "openid", "gateway:access")
			if loginErr != nil {
				return fmt.Errorf("authorize gateway access: %w", loginErr)
			}
			if err := ensureSameIdentity(identity.session, gatewaySession); err != nil {
				return err
			}
			gatewayToken, err = exchangeGatewayToken(ctx, cfg, gatewaySession, credentialClientID)
		}
		if err != nil {
			return err
		}
	}
	cfg.gatewayToken = gatewayToken

	connectURL, err := resolveConnectURL(ctx, cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "  Connect URL source: %s\n", connectURLSource(cfg))

	if shouldOpenBrowser() {
		ui.Event("Hosted connect opened", "Anthropic")
		fmt.Fprintf(out, "  Hosted connect URL: %s\n", connectURL)
		if err := openBrowser(connectURL); err != nil {
			ui.Warning("Browser open failed", "open URL manually")
			ui.Text(connectURL)
			fmt.Fprintf(out, "  Browser open failed: %v\n", err)
		}
	} else {
		ui.Event("Hosted connect ready", "Anthropic")
		ui.Text(connectURL)
		fmt.Fprintln(out, "  Browser open skipped because KONTEXT_DEMO_OPEN_BROWSER=0.")
	}
	if shouldWaitForIntegration() {
		ui.Text("Paste your Anthropic API key in the browser, then press Enter here.")
		if !ui.json {
			fmt.Fprint(ui.out, "  Press Enter after saving the Anthropic key...")
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
			fmt.Fprintln(ui.out)
		}
	}
	fmt.Fprintln(out)
	return nil
}

type demoConnectConfig struct {
	loginClientID     string
	externalClientID  string
	useExternalClient bool
	kontextURL        string
	apiBaseURL        string
	gatewayToken      string
	connectURL        string
}

func demoConnectConfigFromEnv() (demoConnectConfig, error) {
	connectMode := os.Getenv("KONTEXT_DEMO_CONNECT_CLIENT")
	kontextURL := strings.TrimRight(getenv("KONTEXT_URL", defaultKontextURL), "/")
	return demoConnectConfig{
		loginClientID:     getenv("KONTEXT_LOGIN_CLIENT_ID", defaultKontextLoginClientID),
		externalClientID:  os.Getenv("KONTEXT_CLIENT_ID"),
		useExternalClient: connectMode == "external",
		kontextURL:        kontextURL,
		apiBaseURL:        apiBaseURLFromKontextURL(kontextURL),
		gatewayToken:      os.Getenv("KONTEXT_GATEWAY_TOKEN"),
		connectURL:        os.Getenv("KONTEXT_CONNECT_URL"),
	}, nil
}

func resolveConnectURL(ctx context.Context, cfg demoConnectConfig) (string, error) {
	if cfg.connectURL != "" {
		return cfg.connectURL, nil
	}
	if cfg.gatewayToken != "" {
		return fetchConnectURL(ctx, cfg)
	}
	return "", fmt.Errorf("connect URL requires KONTEXT_GATEWAY_TOKEN or KONTEXT_CONNECT_URL")
}

func connectURLSource(cfg demoConnectConfig) string {
	switch {
	case cfg.connectURL != "":
		return "KONTEXT_CONNECT_URL"
	case cfg.gatewayToken != "":
		return "POST " + strings.TrimRight(cfg.apiBaseURL, "/") + "/mcp/connect-session"
	default:
		return "no real server handshake"
	}
}

func apiBaseURLFromKontextURL(kontextURL string) string {
	return strings.TrimSuffix(strings.TrimRight(kontextURL, "/"), "/mcp")
}

func fetchConnectURL(ctx context.Context, cfg demoConnectConfig) (string, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(cfg.apiBaseURL, "/")+"/mcp/connect-session",
		bytes.NewReader([]byte("{}")),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.gatewayToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create connect session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("create connect session failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		ConnectURL string `json:"connectUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode connect session: %w", err)
	}
	if payload.ConnectURL == "" {
		return "", fmt.Errorf("connect session response missing connectUrl")
	}
	return payload.ConnectURL, nil
}

func ensureSameIdentity(active, browser *loginSession) error {
	if active == nil || browser == nil {
		return fmt.Errorf("missing login session for identity check")
	}
	if active.identityKey() == browser.identityKey() {
		return nil
	}
	return fmt.Errorf(
		"browser authorization used a different account (active account: %s; browser account: %s)",
		active.displayIdentity(),
		browser.displayIdentity(),
	)
}

func needsGatewayAccessReauthentication(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "invalid_scope") &&
		strings.Contains(msg, "gateway:access")
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func shouldOpenBrowser() bool {
	return os.Getenv("KONTEXT_DEMO_OPEN_BROWSER") != "0"
}

func shouldShowAuthURL() bool {
	return os.Getenv("KONTEXT_DEMO_SHOW_AUTH_URL") == "1"
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

func shouldWaitForIntegration() bool {
	return os.Getenv("KONTEXT_DEMO_WAIT_FOR_CONNECT") != "0"
}
