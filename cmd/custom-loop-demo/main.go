package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/kontext-security/kontext-go/internal/fakeanthropic"
	"github.com/kontext-security/kontext-go/kontextanthropic"
)

type listFilesInput struct {
	Path string `json:"path" jsonschema:"required,description=Directory or file path to inspect"`
	Note string `json:"note,omitempty" jsonschema:"description=Optional note passed through from the model"`
}

type demoOptions struct {
	connect       bool
	fakeAnthropic bool
	verbose       bool
	json          bool
}

func main() {
	if err := run(context.Background(), parseDemoOptions()); err != nil {
		fmt.Fprintf(os.Stderr, "demo failed: %v\n", err)
		os.Exit(1)
	}
}

func parseDemoOptions() demoOptions {
	var opts demoOptions
	flag.BoolVar(&opts.connect, "connect", false, "open hosted provider connect after creating the Kontext session")
	flag.BoolVar(&opts.fakeAnthropic, "fake-anthropic", os.Getenv("KONTEXT_DEMO_FAKE_ANTHROPIC") == "1", "use deterministic fake Anthropic responses")
	flag.BoolVar(&opts.verbose, "verbose", false, "show auth/session/request/debug details")
	flag.BoolVar(&opts.json, "json", false, "emit machine-readable Kontext events")
	flag.Parse()
	return opts
}

func run(ctx context.Context, opts demoOptions) error {
	ui := newDemoUI(os.Stdout, opts.verbose, opts.json)
	ui.Header("Kontext Go SDK", "Existing Anthropic loop · zero rewrite")
	ui.Section("Setup")

	var identity demoIdentity
	var connectCfg demoConnectConfig
	var err error
	if opts.fakeAnthropic && !opts.connect && os.Getenv("KONTEXT_DEMO_REAL_AUTH") != "1" {
		identity, connectCfg = localDemoIdentity()
	} else {
		identity, connectCfg, err = runKontextLoginDemo(ctx, ui.DebugWriter())
		if err != nil {
			return err
		}
	}
	ui.Success("Kontext authenticated", "")

	kx, err := kontextanthropic.Start(ctx, kontextanthropic.Config{
		ServiceName: "go-agent-poc",
		Environment: "dev",
		APIBaseURL:  identity.APIBaseURL,
		AccessToken: identity.AccessToken,
		UserID:      identity.UserID,
		Agent:       "go-sdk",
		ClientID:    kxClientIDFromEnv(),
		Credentials: kontextanthropic.CredentialsConfig{
			Mode:      kontextanthropic.CredentialModeProvide,
			Providers: []kontextanthropic.Provider{anthropicProviderHandle()},
		},
		Output: kontextOutputMode(opts),
		OnEvent: func(event kontextanthropic.Event) {
			ui.OnKontextEvent(event.Name, event.Record)
		},
	})
	if err != nil {
		return err
	}
	defer kx.End(ctx)
	if opts.verbose && !opts.json {
		fmt.Fprintf(os.Stdout, "  Authenticated through %s as %s.\n", identity.IssuerURL, identity.session.displayIdentity())
		fmt.Fprintln(os.Stdout, "  This session is created through the same AgentService path the CLI uses.")
		if opts.connect {
			fmt.Fprintf(os.Stdout, "  Managed agent client for hosted connect: %s\n", kx.AgentClientID())
		}
		fmt.Fprintln(os.Stdout)
	}

	if opts.connect {
		if err := runHostedConnectDemo(ctx, ui, identity, connectCfg, kx.AgentClientID()); err != nil {
			return err
		}
		if err := verifyConnectedAnthropicCredential(ctx, kx, ui); err != nil {
			return err
		}
		ui.Section("Next")
		ui.Text("env -u ANTHROPIC_API_KEY go run ./cmd/custom-loop-demo")
		ui.Success("Ready", "")
		return nil
	}

	source, err := anthropicCredentialSource(ctx, kx, opts)
	if err != nil {
		if handled := printMissingCredentialSetup(ui, err); handled {
			return nil
		}
		return err
	}
	clientOpts, err := anthropicClientOptions(kx, opts)
	if err != nil {
		return err
	}
	client := anthropic.NewClient(clientOpts...)
	ui.Success("Anthropic credential", "source: "+source)
	ui.Success("Credential protected", "provider key hidden")
	ui.Success("Request telemetry", "enabled")
	ui.Success("Tool boundary", "dispatchTool")

	prompt := `Use list_files once with path ".". Return one short sentence explaining what this Go integration demonstrates.`
	ui.Section("Prompt")
	ui.Text(`Use list_files once with path ".".`)

	ui.Section("Run")
	kx.TrackPrompt(ctx, prompt)
	if _, err := runManualAgentLoop(ctx, kx, client, prompt); err != nil {
		return fmt.Errorf("run manual agent loop: %w", err)
	}

	ui.Section("Result")
	ui.Text("Existing Anthropic loop completed with Kontext telemetry and trace capture.")

	ui.Section("Trace")
	ui.Text("https://app.kontext.security/traces")

	if err := kx.End(ctx); err != nil {
		return err
	}
	ui.Success("Done", "")
	return nil
}

func anthropicCredentialSource(ctx context.Context, kx *kontextanthropic.Client, opts demoOptions) (string, error) {
	if opts.fakeAnthropic {
		return "demo transport", nil
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "environment", nil
	}
	credential, err := kx.ProviderCredential(ctx, anthropicProviderHandle())
	if err != nil {
		return "", err
	}
	return credentialSourceLabel(credential.Source), nil
}

func verifyConnectedAnthropicCredential(ctx context.Context, kx *kontextanthropic.Client, ui *demoUI) error {
	ui.Section("Verify")
	credential, err := kx.ProviderCredential(ctx, anthropicProviderHandle())
	if err != nil {
		ui.Warning("Anthropic credential", "not available yet")
		return fmt.Errorf("verify Anthropic credential exchange: %w", err)
	}
	ui.Success("Anthropic credential saved", "")
	ui.Success("Credential exchange verified", "source: "+credentialSourceLabel(credential.Source))
	return nil
}

func printMissingCredentialSetup(ui *demoUI, err error) bool {
	var connectErr *kontextanthropic.ProviderConnectionRequiredError
	if !errors.As(err, &connectErr) {
		return false
	}

	ui.Warning("Anthropic credential", "missing")
	ui.Section("Next")
	if connectErr.ConnectURL != "" {
		ui.Text("Connect Anthropic:")
		ui.Text(connectErr.ConnectURL)
	} else {
		ui.Text("Connect Anthropic:")
		ui.Text("go run ./cmd/custom-loop-demo --connect")
	}
	ui.Text("Or use your local environment:")
	ui.Text("export ANTHROPIC_API_KEY=...")
	ui.Text("No code changes required.")
	return true
}

func credentialSourceLabel(source string) string {
	switch source {
	case "kontext":
		return "Kontext"
	case "environment":
		return "environment"
	case "request_option":
		return "request option"
	default:
		if source == "" {
			return "unknown"
		}
		return source
	}
}

func kontextOutputMode(opts demoOptions) kontextanthropic.OutputMode {
	switch {
	case opts.json:
		return kontextanthropic.OutputJSON
	case opts.verbose:
		return kontextanthropic.OutputPretty
	default:
		return kontextanthropic.OutputQuiet
	}
}

func runManualAgentLoop(ctx context.Context, kx *kontextanthropic.Client, client anthropic.Client, prompt string) (*anthropic.Message, error) {
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
	}
	tools := []anthropic.ToolUnionParam{{
		OfTool: &anthropic.ToolParam{
			Name:        "list_files",
			Description: anthropic.String("List files in a local directory"),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Directory or file path to inspect",
					},
				},
				Required: []string{"path"},
			},
			Type: anthropic.ToolTypeCustom,
		},
	}}

	for range 3 {
		message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.ModelClaudeSonnet4_5,
			MaxTokens: 1024,
			Messages:  messages,
			Tools:     tools,
		})
		if err != nil {
			return nil, err
		}

		toolResults := make([]anthropic.ContentBlockParamUnion, 0)
		for _, block := range message.Content {
			if block.Type != "tool_use" {
				continue
			}
			toolUse := block.AsToolUse()
			input := decodeToolInput(toolUse.Input)
			result, toolErr := kontextanthropic.ObserveTool(ctx, kx, toolUse.Name, input, func(ctx context.Context) (string, error) {
				return dispatchTool(ctx, toolUse)
			})
			if toolErr != nil {
				result = toolErr.Error()
			}
			toolResults = append(toolResults, anthropic.NewToolResultBlock(toolUse.ID, result, toolErr != nil))
		}

		if len(toolResults) == 0 {
			return message, nil
		}
		messages = append(messages, message.ToParam())
		messages = append(messages, anthropic.NewUserMessage(toolResults...))
	}

	return nil, fmt.Errorf("manual agent loop exceeded max iterations")
}

func dispatchTool(ctx context.Context, toolUse anthropic.ToolUseBlock) (string, error) {
	switch toolUse.Name {
	case "list_files":
		var input listFilesInput
		if err := json.Unmarshal(toolUse.Input, &input); err != nil {
			return "", fmt.Errorf("decode list_files input: %w", err)
		}
		return listFilesText(ctx, input)
	default:
		return "", fmt.Errorf("unknown tool %q", toolUse.Name)
	}
}

func decodeToolInput(input []byte) any {
	var value any
	if err := json.Unmarshal(input, &value); err != nil {
		return string(input)
	}
	return value
}

func listFilesText(ctx context.Context, input listFilesInput) (string, error) {
	_ = ctx

	root, err := os.Getwd()
	if err != nil {
		return "", err
	}

	target := input.Path
	if target == "" {
		target = "."
	}

	clean := filepath.Clean(target)
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("absolute paths are not allowed in this demo")
	}

	fullPath := filepath.Join(root, clean)
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	fullPathAbs, err := filepath.Abs(fullPath)
	if err != nil {
		return "", err
	}
	if fullPathAbs != rootAbs && !strings.HasPrefix(fullPathAbs, rootAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes the demo workspace")
	}

	info, err := os.Stat(fullPathAbs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(fullPathAbs)
		if err != nil {
			return "", err
		}
		preview := string(data)
		if len(preview) > 500 {
			preview = preview[:500]
		}
		text := fmt.Sprintf(
			"path=%s file_bytes=%d preview=%q",
			clean,
			len(data),
			preview,
		)
		return text, nil
	}

	entries, err := os.ReadDir(fullPathAbs)
	if err != nil {
		return "", err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)

	text := fmt.Sprintf(
		"path=%s files=%s",
		clean,
		strings.Join(names, ","),
	)

	return text, nil
}

func assistantText(message *anthropic.Message) string {
	if message == nil {
		return ""
	}

	var out strings.Builder
	for _, block := range message.Content {
		if block.Type == "text" {
			out.WriteString(block.Text)
		}
	}
	return out.String()
}

func shortResult(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if text == "" {
		return "Go SDK agent run captured in Kontext with tool-level telemetry."
	}
	if len(text) <= 180 {
		return text
	}
	return strings.TrimSpace(text[:177]) + "..."
}

func anthropicClientOptions(kx *kontextanthropic.Client, demoOpts demoOptions) ([]option.RequestOption, error) {
	requestOpts := []option.RequestOption{
		kx.WithCredentialsFor(anthropicProviderHandle()),
		kx.WithRequestTelemetry(),
	}
	if demoOpts.fakeAnthropic {
		return append([]option.RequestOption{
			option.WithAPIKey("sk-ant-api03-demoSecretForRedaction"),
			option.WithHTTPClient(fakeanthropic.NewHTTPClient()),
		}, requestOpts...), nil
	}
	return requestOpts, nil
}

func kxClientIDFromEnv() string {
	return os.Getenv("KONTEXT_CLIENT_ID")
}

func anthropicProviderHandle() kontextanthropic.Provider {
	if value := os.Getenv("KONTEXT_PROVIDER_HANDLE"); value != "" {
		return kontextanthropic.Provider(value)
	}
	return kontextanthropic.Provider("anthropic-prod")
}
