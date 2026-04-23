---
name: kontext-go-integrator
description: Detect and minimally patch Go agents using the Anthropic SDK so they use Kontext credentials, request telemetry, prompt tracking, and tool telemetry without changing the existing agent loop.
---

# Kontext Go Integrator

Goal: add Kontext to an existing Anthropic Go agent with the smallest safe diff.

Default behavior:
1. Inspect the Go module and confirm `github.com/anthropics/anthropic-sdk-go` is used.
2. Find `anthropic.NewClient`.
3. Preserve existing credential behavior and add Kontext at the Anthropic client boundary.
4. Add Anthropic request telemetry.
5. Track the user prompt if the prompt variable is obvious.
6. Find the existing tool execution boundary.
7. Wrap that boundary with `ObserveTool`.
8. Run `gofmt`, `go mod tidy`, and `go test ./...`.
9. Report the detected shape, files changed, and verification result.

Integration rules:
- Do not migrate to BetaToolRunner unless explicitly requested.
- Do not rewrite Anthropic message choreography.
- Do not change tool semantics or tool schemas.
- Do not write secrets to files.
- Do not print raw secrets.
- Do not override `ANTHROPIC_API_KEY` unless explicitly configured.
- If the tool boundary is unclear, add credentials and request telemetry only.

Credential patch:
```go
kx, err := kontext.Start(ctx, kontext.Config{
    ServiceName: "customer-agent",
    Environment: "dev",
    Credentials: kontext.CredentialsConfig{
        Mode: kontext.CredentialModeProvide,
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
```

Tool patch:
```go
result, err := kxanthropic.ObserveTool(ctx, kx, toolUse.Name, toolUse.Input, func(ctx context.Context) (ToolResult, error) {
    return dispatchTool(ctx, toolUse)
})
```

ToolRunner fallback:
```go
tools := kxanthropic.WrapTools(kx, existingTools...)
```

Expected report:
```text
Detected
  Go module
  Anthropic SDK
  Manual Messages loop
  Dispatcher: dispatchTool

Applied
  Kontext session start/end
  Anthropic credentials
  Anthropic request telemetry
  Prompt tracking
  ObserveTool around dispatchTool

Verification
  gofmt passed
  go test ./... passed
```
