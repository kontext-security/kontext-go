package kontext

import (
	"context"

	"github.com/kontext-security/kontext-go/kontextanthropic"
)

type Config = kontextanthropic.Config
type Client = kontextanthropic.Client
type CredentialsConfig = kontextanthropic.CredentialsConfig
type CredentialMode = kontextanthropic.CredentialMode
type Provider = kontextanthropic.Provider
type ProviderCredential = kontextanthropic.ProviderCredential
type ProviderConnectionRequiredError = kontextanthropic.ProviderConnectionRequiredError
type Event = kontextanthropic.Event
type OutputMode = kontextanthropic.OutputMode

const (
	CredentialModeObserve  = kontextanthropic.CredentialModeObserve
	CredentialModeProvide  = kontextanthropic.CredentialModeProvide
	CredentialModeOverride = kontextanthropic.CredentialModeOverride
	ProviderAnthropic      = kontextanthropic.ProviderAnthropic
	OutputPretty           = kontextanthropic.OutputPretty
	OutputJSON             = kontextanthropic.OutputJSON
	OutputQuiet            = kontextanthropic.OutputQuiet
)

func Start(ctx context.Context, cfg Config) (*Client, error) {
	return kontextanthropic.Start(ctx, cfg)
}
