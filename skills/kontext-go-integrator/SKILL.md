---
name: kontext-go-integrator
description: Detect and minimally patch Go agents using the Anthropic SDK so they use Kontext credentials, request telemetry, prompt tracking, and tool telemetry without changing the existing agent loop.
---

# Kontext Go Integrator

Goal: add Kontext to an existing Anthropic Go agent with the smallest safe diff.

Canonical user request:
```text
Use the kontext-go-integrator skill to integrate Kontext into this Anthropic Go SDK agent. Preserve the existing loop. Add github.com/kontext-security/kontext-go@v0.1.4, add Kontext session start/end, add WithCredentials(kx) and WithRequestTelemetry(kx), add TrackPrompt if obvious, and wrap the existing tool dispatch boundary with ObserveTool. Then run gofmt, go mod tidy, and go test ./...
```

Default behavior:
1. Inspect the Go module and confirm `github.com/anthropics/anthropic-sdk-go` is used.
2. Find `anthropic.NewClient`.
3. Add the Kontext Go module with `go get github.com/kontext-security/kontext-go@v0.1.4`.
4. Preserve existing credential behavior and add Kontext at the Anthropic client boundary.
5. Add Anthropic request telemetry.
6. Track the user prompt if the prompt variable is obvious.
7. Find the existing tool execution boundary.
8. Wrap that boundary with `ObserveTool`.
9. Run `gofmt`, `go mod tidy`, and `go test ./...`.
10. Report the detected shape, files changed, and verification result.

Reference examples:
- `examples/custom-loop-before.go` -> `examples/custom-loop-after.go`
- `examples/toolrunner-before.go` -> `examples/toolrunner-after.go`
- `examples/telemetry-only-before.go` -> `examples/telemetry-only-after.go`

Integration rules:
- Do not migrate to BetaToolRunner unless explicitly requested.
- Do not rewrite Anthropic message choreography.
- Do not change tool semantics or tool schemas.
- After introducing `kx, err := kontext.Start(...)`, preserve Go scoping by converting later blank-identifier redeclarations like `_, err := call()` to `_, err = call()` when no non-blank variable is newly declared.
- If there is no central dispatcher but a local `switch` or `if` chain executes tool handlers, prefer wrapping that local tool-execution block with one `ObserveTool` callback instead of rewriting individual handler functions.
- If the customer uses a config value such as `cfg.APIKey` or a client factory, preserve the existing explicit `option.WithAPIKey(...)` and add `WithCredentials(kx)` plus `WithRequestTelemetry(kx)` beside it. Do not replace non-env credential sources.
- If `anthropic.NewClient` is hidden behind a local factory, prefer passing `kx *kontext.Client` or a Kontext request option into that factory instead of duplicating client construction at the call site.
- If request options are assembled in a `[]option.RequestOption` slice and passed as `opts...`, preserve existing custom options and insert or append Kontext options inside the slice.
- If an agent struct owns the Anthropic client, add `kx *kontext.Client` to the same owner and close it in the existing run/close lifecycle. Only change constructor signatures when local call sites can be updated safely; otherwise report the ambiguity.
- Do not write secrets to files.
- Do not print raw secrets.
- Do not override `ANTHROPIC_API_KEY` unless explicitly configured.
- If the tool boundary is unclear, add credentials and request telemetry only.

Credential patch:
```go
import (
    kontext "github.com/kontext-security/kontext-go"
    kxanthropic "github.com/kontext-security/kontext-go/anthropic"
)

kx, err := kontext.Start(ctx, kontext.Config{
    ServiceName: "customer-agent",
    Environment: "dev",
    Credentials: kontext.CredentialsConfig{
        Mode: kontext.CredentialModeProvide,
        Providers: []kontext.Provider{"anthropic-prod"},
    },
})
if err != nil {
    return err
}
defer kx.End(ctx)

client := anthropic.NewClient(
    kxanthropic.WithCredentialsFor(kx, "anthropic-prod"),
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
