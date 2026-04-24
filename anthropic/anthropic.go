package anthropic

import (
	"context"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	kontext "github.com/kontext-security/kontext-go"
	"github.com/kontext-security/kontext-go/kontextanthropic"
)

func WithCredentials(kx *kontext.Client) option.RequestOption {
	return kx.WithCredentials()
}

func WithCredentialsFor(kx *kontext.Client, providerHandle string) option.RequestOption {
	return kx.WithCredentialsFor(kontext.Provider(providerHandle))
}

func WithRequestTelemetry(kx *kontext.Client) option.RequestOption {
	return kx.WithRequestTelemetry()
}

func ObserveTool[T any](
	ctx context.Context,
	kx *kontext.Client,
	name string,
	input any,
	fn func(context.Context) (T, error),
) (T, error) {
	return kontextanthropic.ObserveTool(ctx, kx, name, input, fn)
}

func WrapTools(kx *kontext.Client, tools ...anthropicsdk.BetaTool) []anthropicsdk.BetaTool {
	return kx.WrapTools(tools...)
}
