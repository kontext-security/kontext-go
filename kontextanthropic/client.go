package kontextanthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Config describes the Kontext session used by the Anthropic adapter.
type Config struct {
	ServiceName  string
	Environment  string
	APIBaseURL   string
	URL          string
	AccessToken  string
	UserID       string
	Agent        string
	CWD          string
	ClientID     string
	ClientSecret string
	Credentials  CredentialsConfig
	Output       OutputMode
	OnEvent      func(Event)
}

type CredentialsConfig struct {
	Mode      CredentialMode
	Providers []Provider
}

type CredentialMode string

const (
	CredentialModeObserve  CredentialMode = "observe"
	CredentialModeProvide  CredentialMode = "provide"
	CredentialModeOverride CredentialMode = "override"
)

type Provider string

const ProviderAnthropic Provider = "anthropic"

// Client owns Kontext session telemetry and wraps Anthropic Go SDK tools at the
// execution boundary.
type Client struct {
	sessionID       string
	sessionName     string
	agentID         string
	organizationID  string
	cfg             Config
	out             io.Writer
	format          outputFormat
	now             func() time.Time
	mu              sync.Mutex
	ended           bool
	credentialCache map[Provider]ProviderCredential
}

type outputFormat string

const (
	outputPretty outputFormat = "pretty"
	outputJSON   outputFormat = "json"
	outputQuiet  outputFormat = "quiet"
)

type OutputMode string

const (
	OutputPretty OutputMode = "pretty"
	OutputJSON   OutputMode = "json"
	OutputQuiet  OutputMode = "quiet"
)

type Event struct {
	Name   string
	Record map[string]any
}

// Start creates a local Kontext session and emits session.started.
func Start(ctx context.Context, cfg Config) (*Client, error) {
	var err error
	cfg, err = configWithDefaultAuth(ctx, cfg, os.Stdout)
	if err != nil {
		return nil, err
	}
	return start(ctx, cfg, os.Stdout, outputFormatFromConfig(cfg))
}

func startWithWriter(ctx context.Context, cfg Config, out io.Writer) (*Client, error) {
	return start(ctx, cfg, out, outputJSON)
}

func start(ctx context.Context, cfg Config, out io.Writer, format outputFormat) (*Client, error) {
	if cfg.ServiceName == "" {
		return nil, fmt.Errorf("service name is required")
	}
	if cfg.Environment == "" {
		return nil, fmt.Errorf("environment is required")
	}
	if out == nil {
		out = io.Discard
	}

	sessionID := newSessionID()
	sessionName := "local demo session"
	var agentID string
	var organizationID string
	if cfg.AccessToken != "" {
		created, err := createManagedSession(ctx, cfg, sessionID)
		if err != nil {
			return nil, err
		}
		sessionID = created.SessionID
		sessionName = created.SessionName
		agentID = created.AgentID
		organizationID = created.OrganizationID
		if agentID != "" {
			if err := bootstrapManagedAgent(ctx, cfg, agentID); err != nil {
				return nil, err
			}
		}
	}

	c := &Client{
		sessionID:       sessionID,
		sessionName:     sessionName,
		agentID:         agentID,
		organizationID:  organizationID,
		cfg:             cfg,
		out:             out,
		format:          format,
		now:             time.Now,
		credentialCache: make(map[Provider]ProviderCredential),
	}
	c.emit(ctx, "session.started", map[string]any{"session_name": sessionName})
	return c, nil
}

func outputFormatFromConfig(cfg Config) outputFormat {
	switch cfg.Output {
	case OutputJSON:
		return outputJSON
	case OutputQuiet:
		return outputQuiet
	default:
		return outputPretty
	}
}

// AgentID returns the backend application/agent id created for this session.
func (c *Client) AgentID() string {
	return c.agentID
}

// AgentClientID returns the OAuth client id for the managed agent, matching the CLI's app_<agentID> convention.
func (c *Client) AgentClientID() string {
	if c.agentID == "" {
		return ""
	}
	return "app_" + c.agentID
}

// End emits session.ended. It is safe to call more than once.
func (c *Client) End(ctx context.Context) error {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return nil
	}
	c.ended = true
	c.mu.Unlock()

	fields := map[string]any{}
	if c.cfg.AccessToken != "" {
		if err := endManagedSession(ctx, c.cfg, c.sessionID); err != nil {
			fields["backend_error"] = err.Error()
		}
	}
	c.emit(ctx, "session.ended", fields)
	return nil
}

// TrackPrompt emits the Go-agent equivalent of Claude Code's UserPromptSubmit hook.
func (c *Client) TrackPrompt(ctx context.Context, prompt string) {
	c.emit(ctx, "prompt.submitted", map[string]any{
		"prompt": prompt,
	})
}

// WithRequestTelemetry adds Anthropic request/response telemetry middleware.
func (c *Client) WithRequestTelemetry() option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		started := c.now()
		body := readRequestBody(req)
		model := extractModel(body)

		c.emit(req.Context(), "anthropic.request.started", map[string]any{
			"method":  req.Method,
			"path":    req.URL.Path,
			"model":   model,
			"headers": redactHeaders(req.Header),
		})

		resp, err := next(req)

		fields := map[string]any{
			"duration_ms": durationMilliseconds(c.now().Sub(started)),
		}
		if resp != nil {
			fields["status"] = resp.StatusCode
			if requestID := responseRequestID(resp.Header); requestID != "" {
				fields["request_id"] = requestID
			}
		}
		if err != nil {
			fields["error"] = err.Error()
		}

		c.emit(req.Context(), "anthropic.request.completed", fields)
		return resp, err
	})
}

// WithCredentials resolves the Anthropic API key without writing it to disk.
// Existing ANTHROPIC_API_KEY or explicit Anthropic request options win unless
// CredentialModeOverride is configured.
func (c *Client) WithCredentials() option.RequestOption {
	return c.WithCredentialsFor(ProviderAnthropic)
}

// WithCredentialsFor resolves the Anthropic API key from a specific Kontext
// provider handle without writing it to disk.
func (c *Client) WithCredentialsFor(provider Provider) option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		mode := c.credentialMode(provider)
		if mode != CredentialModeOverride {
			if req.Header.Get("X-Api-Key") != "" || req.Header.Get("Authorization") != "" {
				source := "request_option"
				if os.Getenv("ANTHROPIC_API_KEY") != "" {
					source = "environment"
				}
				c.emit(req.Context(), "provider.credential.resolved", map[string]any{
					"provider": provider,
					"source":   source,
				})
				return next(req)
			}
			if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
				req.Header.Set("X-Api-Key", key)
				c.emit(req.Context(), "provider.credential.resolved", map[string]any{
					"provider": provider,
					"source":   "environment",
				})
				return next(req)
			}
		}

		if mode == CredentialModeObserve {
			c.emit(req.Context(), "provider.credential.missing", map[string]any{
				"provider": provider,
				"source":   "missing",
			})
			return nil, fmt.Errorf("ANTHROPIC_API_KEY is required; set it in the environment or enable Kontext credential mode provide")
		}

		credential, err := c.ProviderCredential(req.Context(), provider)
		if err != nil {
			c.emit(req.Context(), "provider.credential.missing", map[string]any{
				"provider": provider,
				"source":   "kontext",
				"error":    err.Error(),
			})
			return nil, err
		}
		req.Header.Set("X-Api-Key", credential.Value)
		c.emit(req.Context(), "provider.credential.resolved", map[string]any{
			"provider": credential.Provider,
			"source":   credential.Source,
		})
		return next(req)
	})
}

// WrapTools wraps Anthropic beta tools so Kontext observes actual execution.
func (c *Client) WrapTools(tools ...anthropic.BetaTool) []anthropic.BetaTool {
	wrapped := make([]anthropic.BetaTool, 0, len(tools))
	for _, tool := range tools {
		wrapped = append(wrapped, c.WrapTool(tool))
	}
	return wrapped
}

// WrapTool wraps a single Anthropic beta tool.
func (c *Client) WrapTool(tool anthropic.BetaTool) anthropic.BetaTool {
	return &wrappedTool{client: c, inner: tool}
}

func ObserveTool[T any](ctx context.Context, c *Client, name string, input any, fn func(context.Context) (T, error)) (T, error) {
	started := c.now()
	toolUseID := "go_tool_" + fmt.Sprint(c.now().UnixNano())
	c.emit(ctx, "tool.pre_use", map[string]any{
		"tool_name":   name,
		"tool_use_id": toolUseID,
		"input":       input,
	})

	output, err := fn(ctx)

	fields := map[string]any{
		"tool_name":   name,
		"tool_use_id": toolUseID,
		"input":       input,
		"output":      output,
		"duration_ms": durationMilliseconds(c.now().Sub(started)),
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	c.emit(ctx, "tool.post_use", fields)
	return output, err
}

func (c *Client) credentialMode(provider Provider) CredentialMode {
	mode := c.cfg.Credentials.Mode
	if mode == "" {
		return CredentialModeObserve
	}
	if mode == CredentialModeObserve {
		return mode
	}
	for _, configured := range c.cfg.Credentials.Providers {
		if configured == provider {
			return mode
		}
	}
	return CredentialModeObserve
}

func (c *Client) emit(ctx context.Context, event string, fields map[string]any) {
	record := map[string]any{
		"event":        event,
		"timestamp":    c.now().UTC().Format(time.RFC3339Nano),
		"session_id":   c.sessionID,
		"service_name": c.cfg.ServiceName,
		"environment":  c.cfg.Environment,
	}
	for key, value := range fields {
		record[key] = redactValue(value)
	}
	if err := ctx.Err(); err != nil {
		record["context_error"] = err.Error()
	}
	if err := c.ingestHookEvent(ctx, event, fields); err != nil {
		record["backend_error"] = err.Error()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfg.OnEvent != nil {
		c.cfg.OnEvent(Event{Name: event, Record: record})
	}
	switch c.format {
	case outputPretty:
		_, _ = fmt.Fprintln(c.out, prettyEventLine(event, record))
	case outputJSON:
		_ = json.NewEncoder(c.out).Encode(record)
	}
}

type wrappedTool struct {
	client *Client
	inner  anthropic.BetaTool
}

func (t *wrappedTool) Name() string {
	return t.inner.Name()
}

func (t *wrappedTool) Description() string {
	return t.inner.Description()
}

func (t *wrappedTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return t.inner.InputSchema()
}

func (t *wrappedTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	started := t.client.now()
	inputValue := decodeJSONValue(input)
	toolUseID := "go_tool_" + fmt.Sprint(t.client.now().UnixNano())

	t.client.emit(ctx, "tool.pre_use", map[string]any{
		"tool_name":   t.inner.Name(),
		"tool_use_id": toolUseID,
		"input":       inputValue,
	})

	output, err := t.inner.Execute(ctx, input)

	fields := map[string]any{
		"tool_name":   t.inner.Name(),
		"tool_use_id": toolUseID,
		"input":       inputValue,
		"output":      decodeJSONValue(mustMarshal(output)),
		"duration_ms": durationMilliseconds(t.client.now().Sub(started)),
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	t.client.emit(ctx, "tool.post_use", fields)

	return output, err
}

func readRequestBody(req *http.Request) []byte {
	if req == nil || req.Body == nil {
		return nil
	}

	if req.GetBody != nil {
		body, err := req.GetBody()
		if err == nil {
			defer body.Close()
			data, _ := io.ReadAll(body)
			return data
		}
	}

	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil
	}
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return data
}

func extractModel(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Model
}

func redactHeaders(headers http.Header) map[string]any {
	out := make(map[string]any)
	for key, values := range headers {
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}
	return redactValue(out).(map[string]any)
}

func responseRequestID(headers http.Header) string {
	for _, name := range []string{"request-id", "x-request-id", "anthropic-request-id"} {
		if value := headers.Get(name); value != "" {
			return value
		}
		if values := headers[name]; len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func decodeJSONValue(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return RedactString(string(data))
	}
	return value
}

func mustMarshal(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return []byte(fmt.Sprintf(`{"marshal_error":%q}`, err.Error()))
	}
	return data
}

func durationMilliseconds(duration time.Duration) int64 {
	if duration < time.Millisecond {
		return 0
	}
	return duration.Milliseconds()
}

func newSessionID() string {
	return fmt.Sprintf("kx_%d", time.Now().UnixNano())
}
