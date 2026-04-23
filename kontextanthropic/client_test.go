package kontextanthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/toolrunner"
	"github.com/kontext-security/kontext-go/internal/fakeanthropic"
)

func TestWrapToolEmitsPrePostAndPreservesOutput(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	kx, err := startWithWriter(context.Background(), Config{
		ServiceName: "test-agent",
		Environment: "test",
	}, &out)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	tool := kx.WrapTool(stubTool{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"token":"ghp_demoSecret123456789","command":"echo ok"}`))
	if err != nil {
		t.Fatalf("execute wrapped tool: %v", err)
	}

	if len(result) != 1 || result[0].OfText == nil {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !strings.Contains(result[0].OfText.Text, "GITHUB_TOKEN=ghp_originalToolResult") {
		t.Fatalf("tool output was mutated: %q", result[0].OfText.Text)
	}

	events := decodeEvents(t, out.String())
	requireEvent(t, events, "tool.pre_use")
	requireEvent(t, events, "tool.post_use")

	logged := out.String()
	if strings.Contains(logged, "ghp_demoSecret123456789") || strings.Contains(logged, "ghp_originalToolResult") {
		t.Fatalf("telemetry leaked a fake secret:\n%s", logged)
	}
	if !strings.Contains(logged, redacted) {
		t.Fatalf("telemetry did not show redaction:\n%s", logged)
	}
}

func TestBetaToolRunnerFlowEmitsExpectedEvents(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	ctx := context.Background()
	kx, err := startWithWriter(ctx, Config{
		ServiceName: "go-agent-poc",
		Environment: "test",
	}, &out)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	client := anthropic.NewClient(
		option.WithAPIKey("sk-ant-api03-demoSecretForTest"),
		option.WithHTTPClient(fakeanthropic.NewHTTPClient()),
		kx.WithRequestTelemetry(),
	)

	tool, err := toolrunner.NewBetaToolFromJSONSchema(
		"list_files",
		"List files in a local directory",
		func(ctx context.Context, input listFilesInput) (anthropic.BetaToolResultBlockParamContentUnion, error) {
			return anthropic.BetaToolResultBlockParamContentUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: "files=README.md provider_probe=ANTHROPIC_API_KEY=sk-ant-api03-demoSecret"},
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("create tool: %v", err)
	}

	runner := client.Beta.Messages.NewToolRunner(kx.WrapTools(tool), anthropic.BetaToolRunnerParams{
		BetaMessageNewParams: anthropic.BetaMessageNewParams{
			Model:     anthropic.ModelClaudeSonnet4_5,
			MaxTokens: 1024,
			Messages: []anthropic.BetaMessageParam{
				anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("Inspect this repo.")),
			},
		},
		MaxIterations: 3,
	})

	message, err := runner.RunToCompletion(ctx)
	if err != nil {
		t.Fatalf("run to completion: %v", err)
	}
	if message == nil || len(message.Content) == 0 || !strings.Contains(message.Content[0].Text, "Existing Anthropic loop completed") {
		t.Fatalf("unexpected final message: %#v", message)
	}

	if err := kx.End(ctx); err != nil {
		t.Fatalf("end session: %v", err)
	}

	events := decodeEvents(t, out.String())
	for _, name := range []string{
		"session.started",
		"anthropic.request.started",
		"anthropic.request.completed",
		"tool.pre_use",
		"tool.post_use",
		"session.ended",
	} {
		requireEvent(t, events, name)
	}

	logged := out.String()
	for _, secret := range []string{
		"sk-ant-api03-demoSecretForTest",
		"sk-ant-api03-demoSecret",
		"ghp_demoSecret123456789",
	} {
		if strings.Contains(logged, secret) {
			t.Fatalf("telemetry leaked %q:\n%s", secret, logged)
		}
	}
}

func TestWithCredentialsUsesEnvironmentWithoutLeakingKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-envSecretForTest")

	var out bytes.Buffer
	kx, err := startWithWriter(context.Background(), Config{
		ServiceName: "test-agent",
		Environment: "test",
		Credentials: CredentialsConfig{
			Mode:      CredentialModeProvide,
			Providers: []Provider{ProviderAnthropic},
		},
	}, &out)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	client := anthropic.NewClient(
		option.WithHTTPClient(fakeanthropic.NewHTTPClient()),
		kx.WithCredentials(),
		kx.WithRequestTelemetry(),
	)
	_, err = client.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_5,
		MaxTokens: 64,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("Use list_files once.")),
		},
	})
	if err != nil {
		t.Fatalf("message call: %v", err)
	}

	logged := out.String()
	if !strings.Contains(logged, `"event":"provider.credential.resolved"`) ||
		!strings.Contains(logged, `"source":"environment"`) {
		t.Fatalf("missing environment credential event:\n%s", logged)
	}
	if strings.Contains(logged, "sk-ant-api03-envSecretForTest") {
		t.Fatalf("telemetry leaked env key:\n%s", logged)
	}
}

func TestProviderCredentialExchangesAnthropicKey(t *testing.T) {
	var gotResource string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/kontext.agent.v1.AgentService/CreateSession":
			_, _ = w.Write([]byte(`{"sessionId":"kx_test","sessionName":"test","agentId":"agent_test","organizationId":"org_test"}`))
		case "/kontext.agent.v1.AgentService/BootstrapCli":
			_, _ = w.Write([]byte(`{}`))
		case "/oauth2/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			gotResource = r.Form.Get("resource")
			_, _ = w.Write([]byte(`{
				"access_token":"sk-ant-api03-kontextSecretForTest",
				"provider_kind":"key",
				"provider_handle":"anthropic",
				"token_type":"Bearer"
			}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	kx, err := startWithWriter(context.Background(), Config{
		ServiceName: "test-agent",
		Environment: "test",
		APIBaseURL:  server.URL,
		AccessToken: "kx_user_token",
		ClientID:    "app_test",
		Credentials: CredentialsConfig{
			Mode:      CredentialModeProvide,
			Providers: []Provider{ProviderAnthropic},
		},
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	credential, err := kx.ProviderCredential(context.Background(), ProviderAnthropic)
	if err != nil {
		t.Fatalf("provider credential: %v", err)
	}
	if gotResource != "anthropic" {
		t.Fatalf("resource = %q, want anthropic", gotResource)
	}
	if credential.Value != "sk-ant-api03-kontextSecretForTest" ||
		credential.Source != "kontext" ||
		credential.Kind != "key" {
		t.Fatalf("unexpected credential: %#v", credential)
	}
}

func TestStartRequiresConfidentialRuntimeEnv(t *testing.T) {
	t.Setenv("KONTEXT_CLIENT_ID", "")
	t.Setenv("KONTEXT_CLIENT_SECRET", "")
	t.Setenv("KONTEXT_ACCESS_TOKEN", "")
	t.Setenv("KONTEXT_LOCAL_SESSION", "")

	_, err := Start(context.Background(), Config{
		ServiceName: "test-agent",
		Environment: "test",
	})
	if err == nil {
		t.Fatal("expected missing setup error")
	}
	if !strings.Contains(err.Error(), "KONTEXT_CLIENT_ID") ||
		!strings.Contains(err.Error(), "KONTEXT_CLIENT_SECRET") ||
		!strings.Contains(err.Error(), "KONTEXT_URL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProviderCredentialUsesConfidentialClientExchange(t *testing.T) {
	var gotSubjectToken string
	var gotSubjectTokenType string
	var gotClientSecret string
	var gotResource string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotSubjectToken = r.Form.Get("subject_token")
		gotSubjectTokenType = r.Form.Get("subject_token_type")
		gotClientSecret = r.Form.Get("client_secret")
		gotResource = r.Form.Get("resource")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token":"sk-ant-api03-kontextSecretForTest",
			"provider_kind":"key",
			"provider_handle":"anthropic-prod",
			"token_type":"Bearer"
		}`))
	}))
	defer server.Close()

	kx, err := Start(context.Background(), Config{
		ServiceName:  "test-agent",
		Environment:  "test",
		URL:          server.URL + "/mcp",
		ClientID:     "app_test",
		ClientSecret: "secret_test",
		Credentials: CredentialsConfig{
			Mode:      CredentialModeProvide,
			Providers: []Provider{"anthropic-prod"},
		},
		Output: OutputQuiet,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	credential, err := kx.ProviderCredential(context.Background(), Provider("anthropic-prod"))
	if err != nil {
		t.Fatalf("provider credential: %v", err)
	}
	if gotSubjectToken != "" {
		t.Fatalf("subject_token = %q, want empty", gotSubjectToken)
	}
	if gotSubjectTokenType != confidentialClientTokenType {
		t.Fatalf("subject_token_type = %q, want %q", gotSubjectTokenType, confidentialClientTokenType)
	}
	if gotClientSecret != "secret_test" {
		t.Fatalf("client_secret = %q", gotClientSecret)
	}
	if gotResource != "anthropic-prod" {
		t.Fatalf("resource = %q, want anthropic-prod", gotResource)
	}
	if credential.Provider != "anthropic-prod" || credential.Value == "" {
		t.Fatalf("unexpected credential: %#v", credential)
	}
}

type listFilesInput struct {
	Path string `json:"path" jsonschema:"required,description=Directory path to inspect"`
	Note string `json:"note,omitempty" jsonschema:"description=Optional note passed through from the model"`
}

type stubTool struct{}

func (stubTool) Name() string {
	return "stub"
}

func (stubTool) Description() string {
	return "test stub"
}

func (stubTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{}
}

func (stubTool) Execute(context.Context, json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	return []anthropic.BetaToolResultBlockParamContentUnion{{
		OfText: &anthropic.BetaTextBlockParam{Text: "ok GITHUB_TOKEN=ghp_originalToolResult"},
	}}, nil
}

func decodeEvents(t *testing.T, output string) []map[string]any {
	t.Helper()

	var events []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("decode event %q: %v", string(line), err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan events: %v", err)
	}
	return events
}

func requireEvent(t *testing.T, events []map[string]any, name string) {
	t.Helper()
	for _, event := range events {
		if event["event"] == name {
			return
		}
	}
	t.Fatalf("missing event %q in %#v", name, events)
}
