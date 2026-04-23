//go:build ignore

package main

import (
	"context"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/toolrunner"
	kontext "github.com/kontext-security/kontext-go"
	kxanthropic "github.com/kontext-security/kontext-go/anthropic"
)

func main() error {
	ctx := context.Background()

	// 1. Start a Kontext session for this Go agent run.
	kx, err := kontext.Start(ctx, kontext.Config{
		ServiceName: "go-agent-poc",
		Environment: "dev",
		Credentials: kontext.CredentialsConfig{
			Mode:      kontext.CredentialModeProvide,
			Providers: []kontext.Provider{kontext.ProviderAnthropic},
		},
	})
	if err != nil {
		return err
	}
	defer kx.End(ctx)

	// 2. Keep the official Anthropic Go SDK client, but add Kontext credentials and telemetry.
	client := anthropic.NewClient(
		kxanthropic.WithCredentials(kx),
		kxanthropic.WithRequestTelemetry(kx),
	)

	// 3. Define your normal Go tool.
	listFilesTool, err := toolrunner.NewBetaToolFromJSONSchema(
		"list_files",
		"List files in a local directory",
		listFiles,
	)
	if err != nil {
		return err
	}

	// 4. Wrap the tool boundary so Kontext can emit PreToolUse/PostToolUse.
	tools := kxanthropic.WrapTools(kx, listFilesTool)

	prompt := `Use list_files once with path ".".`
	kx.TrackPrompt(ctx, prompt)

	// 5. Run the existing Anthropic Go SDK agent loop unchanged.
	runner := client.Beta.Messages.NewToolRunner(tools, anthropic.BetaToolRunnerParams{
		BetaMessageNewParams: anthropic.BetaMessageNewParams{
			Model:     anthropic.ModelClaudeSonnet4_5,
			MaxTokens: 1024,
			Messages: []anthropic.BetaMessageParam{
				anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt)),
			},
		},
	})

	_, err = runner.RunToCompletion(ctx)
	return err
}
