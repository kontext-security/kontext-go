//go:build ignore

package main

import (
	"context"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	kontext "github.com/kontext-security/kontext-go"
	kxanthropic "github.com/kontext-security/kontext-go/anthropic"
)

func runAgent(ctx context.Context, prompt string) error {
	kx, err := kontext.Start(ctx, kontext.Config{
		ServiceName: "customer-agent",
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

	client := anthropic.NewClient(
		kxanthropic.WithCredentials(kx),
		kxanthropic.WithRequestTelemetry(kx),
	)
	kx.TrackPrompt(ctx, prompt)

	_, err = client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_5,
		MaxTokens: 1024,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	return err
}
