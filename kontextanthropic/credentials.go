package kontextanthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const tokenExchangeGrantType = "urn:ietf:params:oauth:grant-type:token-exchange"
const accessTokenType = "urn:ietf:params:oauth:token-type:access_token"
const confidentialClientTokenType = "urn:kontext:confidential-client"

type ProviderCredential struct {
	Provider Provider
	Kind     string
	Value    string
	Source   string
}

type ProviderConnectionRequiredError struct {
	Provider   Provider
	ProviderID string
	ConnectURL string
	Message    string
}

func (e *ProviderConnectionRequiredError) Error() string {
	if e == nil {
		return ""
	}
	if e.ConnectURL != "" {
		return fmt.Sprintf("%s; connect provider at %s", e.Message, e.ConnectURL)
	}
	return e.Message
}

func (c *Client) ProviderCredential(ctx context.Context, provider Provider) (ProviderCredential, error) {
	c.mu.Lock()
	if cached, ok := c.credentialCache[provider]; ok {
		c.mu.Unlock()
		return cached, nil
	}
	c.mu.Unlock()

	clientID := c.AgentClientID()
	if clientID == "" {
		clientID = c.cfg.ClientID
	}
	if clientID == "" {
		clientID = os.Getenv("KONTEXT_CLIENT_ID")
	}
	if clientID == "" {
		return ProviderCredential{}, fmt.Errorf("Kontext client id is required to resolve provider %q", provider)
	}
	if c.cfg.AccessToken == "" && c.cfg.ClientSecret == "" {
		return ProviderCredential{}, fmt.Errorf("Kontext client secret is required to resolve provider %q; set KONTEXT_CLIENT_SECRET from the Get Started with Kontext browser setup page", provider)
	}

	credential, err := c.exchangeProviderCredential(ctx, provider, clientID)
	if err != nil {
		return ProviderCredential{}, err
	}

	c.mu.Lock()
	c.credentialCache[provider] = credential
	c.mu.Unlock()
	return credential, nil
}

func (c *Client) exchangeProviderCredential(ctx context.Context, provider Provider, clientID string) (ProviderCredential, error) {
	body := url.Values{}
	body.Set("grant_type", tokenExchangeGrantType)
	body.Set("resource", string(provider))
	body.Set("client_id", clientID)
	if c.cfg.ClientSecret != "" {
		body.Set("client_secret", c.cfg.ClientSecret)
		body.Set("subject_token_type", confidentialClientTokenType)
	} else {
		body.Set("subject_token", c.cfg.AccessToken)
		body.Set("subject_token_type", accessTokenType)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(c.cfg.APIBaseURL, "/")+"/oauth2/token",
		strings.NewReader(body.Encode()),
	)
	if err != nil {
		return ProviderCredential{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ProviderCredential{}, fmt.Errorf("exchange provider %q credential: %w", provider, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ProviderCredential{}, fmt.Errorf("read provider %q credential response: %w", provider, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ProviderCredential{}, c.providerExchangeError(ctx, provider, data, resp.Status)
	}

	var payload struct {
		AccessToken    string `json:"access_token"`
		ProviderKind   string `json:"provider_kind"`
		ProviderHandle string `json:"provider_handle"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ProviderCredential{}, fmt.Errorf("decode provider %q credential response: %w", provider, err)
	}
	if payload.AccessToken == "" {
		return ProviderCredential{}, fmt.Errorf("provider %q credential response missing access_token", provider)
	}
	if payload.ProviderKind != "" && payload.ProviderKind != "key" {
		return ProviderCredential{}, fmt.Errorf("provider %q returned unsupported credential kind %q", provider, payload.ProviderKind)
	}

	resolvedProvider := provider
	if payload.ProviderHandle != "" {
		resolvedProvider = Provider(payload.ProviderHandle)
	}
	return ProviderCredential{
		Provider: resolvedProvider,
		Kind:     "key",
		Value:    payload.AccessToken,
		Source:   "kontext",
	}, nil
}

func (c *Client) providerExchangeError(ctx context.Context, provider Provider, data []byte, status string) error {
	var payload struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		ProviderName     string `json:"provider_name"`
		ProviderID       string `json:"provider_id"`
	}
	_ = json.Unmarshal(data, &payload)

	message := strings.TrimSpace(payload.ErrorDescription)
	if message == "" {
		message = strings.TrimSpace(string(data))
	}
	if message == "" {
		message = "provider credential exchange failed: " + status
	}

	switch payload.Error {
	case "provider_required", "provider_reauthorization_required":
		connectURL, _ := c.fetchConnectURL(ctx)
		return &ProviderConnectionRequiredError{
			Provider:   provider,
			ProviderID: payload.ProviderID,
			ConnectURL: connectURL,
			Message:    message,
		}
	default:
		return fmt.Errorf("provider %q credential exchange failed: %s", provider, message)
	}
}

func (c *Client) fetchConnectURL(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(c.cfg.APIBaseURL, "/")+"/mcp/connect-session",
		strings.NewReader("{}"),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("connect session failed: %s", resp.Status)
	}

	var payload struct {
		ConnectURL string `json:"connectUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	return payload.ConnectURL, nil
}
