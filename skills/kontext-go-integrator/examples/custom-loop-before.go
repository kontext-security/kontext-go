//go:build ignore

package main

import (
	"context"
	"os"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

func runAgent(ctx context.Context, prompt string) error {
	client := anthropic.NewClient(
		option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
	)

	message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_5,
		MaxTokens: 1024,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return err
	}

	for _, block := range message.Content {
		if block.Type != "tool_use" {
			continue
		}
		toolUse := block.AsToolUse()
		_, err := dispatchTool(ctx, toolUse)
		if err != nil {
			return err
		}
	}
	return nil
}

func dispatchTool(context.Context, anthropic.ToolUseBlock) (string, error) {
	return "", nil
}
