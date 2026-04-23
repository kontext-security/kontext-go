package kontextanthropic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
)

type createSessionResponse struct {
	SessionID      string `json:"sessionId"`
	SessionName    string `json:"sessionName"`
	AgentID        string `json:"agentId"`
	OrganizationID string `json:"organizationId"`
}

func createManagedSession(ctx context.Context, cfg Config, fallbackSessionID string) (*createSessionResponse, error) {
	hostname, _ := os.Hostname()
	cwd := cfg.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	var payload struct {
		UserID     string            `json:"userId"`
		Agent      string            `json:"agent"`
		Hostname   string            `json:"hostname"`
		CWD        string            `json:"cwd"`
		ClientInfo map[string]string `json:"clientInfo"`
	}
	payload.UserID = cfg.UserID
	payload.Agent = agentName(cfg)
	payload.Hostname = hostname
	payload.CWD = cwd
	payload.ClientInfo = map[string]string{
		"name":        "kontext-go",
		"os":          runtime.GOOS,
		"sdk":         "anthropic-sdk-go",
		"integration": "tool-boundary-wrapper",
	}

	var resp createSessionResponse
	if err := connectUnary(ctx, cfg, "/kontext.agent.v1.AgentService/CreateSession", payload, &resp); err != nil {
		return nil, fmt.Errorf("create managed session: %w", err)
	}
	if resp.SessionID == "" {
		resp.SessionID = fallbackSessionID
	}
	if resp.SessionName == "" {
		resp.SessionName = "Go SDK demo session"
	}
	return &resp, nil
}

func bootstrapManagedAgent(ctx context.Context, cfg Config, agentID string) error {
	payload := struct {
		AgentID string `json:"agentId"`
	}{AgentID: agentID}
	var resp map[string]any
	if err := connectUnary(ctx, cfg, "/kontext.agent.v1.AgentService/BootstrapCli", payload, &resp); err != nil {
		return fmt.Errorf("bootstrap managed agent: %w", err)
	}
	return nil
}

func endManagedSession(ctx context.Context, cfg Config, sessionID string) error {
	payload := struct {
		SessionID string `json:"sessionId"`
	}{SessionID: sessionID}
	var resp map[string]any
	return connectUnary(ctx, cfg, "/kontext.agent.v1.AgentService/EndSession", payload, &resp)
}

func (c *Client) ingestHookEvent(ctx context.Context, event string, fields map[string]any) error {
	if c.cfg.AccessToken == "" {
		return nil
	}

	hookEvent := hookEventName(event)
	if hookEvent == "" {
		return nil
	}

	cwd := c.cfg.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	payload := map[string]any{
		"sessionId": c.sessionID,
		"agent":     agentName(c.cfg),
		"hookEvent": hookEvent,
		"cwd":       cwd,
	}
	if toolName, _ := fields["tool_name"].(string); toolName != "" {
		payload["toolName"] = toolName
	}
	if toolUseID, _ := fields["tool_use_id"].(string); toolUseID != "" {
		payload["toolUseId"] = toolUseID
	}
	if input, ok := fields["input"]; ok {
		payload["toolInput"] = base64JSON(input)
	}
	if prompt, ok := fields["prompt"]; ok {
		payload["toolInput"] = base64JSON(map[string]any{"prompt": prompt})
	}
	if output, ok := fields["output"]; ok {
		payload["toolResponse"] = base64JSON(output)
	}

	var resp map[string]any
	return connectUnary(ctx, c.cfg, "/kontext.agent.v1.AgentService/ProcessHookEvent", payload, &resp)
}

func connectUnary(ctx context.Context, cfg Config, procedure string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.APIBaseURL, "/")+procedure, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s failed: %s: %s", procedure, resp.Status, strings.TrimSpace(string(data)))
	}

	if out == nil {
		return nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func hookEventName(event string) string {
	switch event {
	case "prompt.submitted":
		return "UserPromptSubmit"
	case "tool.pre_use":
		return "PreToolUse"
	case "tool.post_use":
		return "PostToolUse"
	default:
		return ""
	}
}

func agentName(cfg Config) string {
	if cfg.Agent != "" {
		return cfg.Agent
	}
	return "go-sdk"
}

func base64JSON(value any) string {
	data, err := json.Marshal(redactValue(value))
	if err != nil {
		data = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	return base64.StdEncoding.EncodeToString(data)
}
