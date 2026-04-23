# Kontext Go SDK

This module adds Kontext credentials, request telemetry, prompt tracking, and tool-boundary telemetry to Anthropic Go SDK agents without requiring a CLI wrapper or a migration to ToolRunner.

## Install

The Go package and the skill are separate:

- The Go package is runtime code that gets compiled into the customer's agent.
- The skill is a coding-agent instruction bundle that patches the customer's repo to use the Go package.

The Go module has two public packages:

- `github.com/kontext-security/kontext-go` is the core package for Kontext session, credentials, and prompt tracking.
- `github.com/kontext-security/kontext-go/anthropic` is the Anthropic adapter package for `WithCredentials`, `WithRequestTelemetry`, `ObserveTool`, and `WrapTools`.

In short: one installable Go module, two import paths, one separate skills repo.

Install the Go package:

```sh
go get github.com/kontext-security/kontext-go@v0.1.4
```

Install the skill from the Kontext skills repository:

```sh
npx skills add kontext-security/skills
```

Then ask the coding agent:

```text
Use the kontext-go-integrator skill to integrate Kontext into this Anthropic Go SDK agent. Preserve the existing loop. Add github.com/kontext-security/kontext-go@v0.1.4, add Kontext session start/end, add WithCredentials(kx) and WithRequestTelemetry(kx), add TrackPrompt if obvious, and wrap the existing tool dispatch boundary with ObserveTool. Then run gofmt, go mod tidy, and go test ./...
```

Or install only the Go integrator zip:

```sh
curl -fsSL https://raw.githubusercontent.com/kontext-security/kontext-go/main/scripts/install-skill.sh | sh
```

Or download the skill zip from the GitHub release:

```sh
curl -L -o kontext-go-integrator.zip \
  https://github.com/kontext-security/kontext-go/releases/download/v0.1.4/kontext-go-integrator.zip
```

Fallbacks for live setup:

- Skill zip: `https://github.com/kontext-security/kontext-go/releases/download/v0.1.4/kontext-go-integrator.zip`
- Runtime install: `go get github.com/kontext-security/kontext-go@v0.1.4`
- Demo connect: `go run ./cmd/custom-loop-demo --connect`
- Live proof: `env -u ANTHROPIC_API_KEY go run ./cmd/custom-loop-demo`

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

result, err := kxanthropic.ObserveTool(ctx, kx, toolUse.Name, toolUse.Input, func(ctx context.Context) (string, error) {
	return dispatchTool(ctx, toolUse)
})
```

The demo uses the real Anthropic Go SDK, a manual `Messages.New` loop, and `option.WithMiddleware`. By default it makes a real Anthropic request so the video is product-real end to end. A deterministic Anthropic transport is available for tests and offline recording.

The default flow is the client-facing DevX story:

1. Open browser PKCE login.
2. Create a governed AgentService session.
3. Resolve the Anthropic credential from env or Kontext.
4. Run the existing Go SDK loop and dispatcher.
5. Send `UserPromptSubmit`, credential source, Anthropic request telemetry, `PreToolUse`, and `PostToolUse` events to Kontext.
6. Open Traces and verify the run.

For recording, open `examples/custom-loop-after.go` first. It shows the small integration surface without the demo's auth/bootstrap plumbing.

The optional `--connect` flow mirrors the hosted-connect side of `kontext-cli`:

1. Use the managed agent client returned by `CreateSession` for gateway access.
2. Create a short-lived hosted connect session through `POST /mcp/connect-session`.
3. Open the real integration page returned by the backend.

The demo defaults to the CLI-compatible login path:

```sh
KONTEXT_LOGIN_CLIENT_ID=app_a4fb6d20-e937-450f-aa19-db585405aa92
KONTEXT_URL=https://api.kontext.security/mcp
```

`KONTEXT_CLIENT_ID` from an application dashboard is not used for the initial PKCE login. That dashboard application client is for gateway/application auth. The live traces backend currently trusts the CLI login client for AgentService telemetry, then the demo uses the managed `app_<agentID>` client returned by `CreateSession` for hosted connect. If you deliberately want to try an external app client for hosted connect, set:

```sh
KONTEXT_CLIENT_ID=app_...
KONTEXT_DEMO_CONNECT_CLIENT=external
```

That external client must be configured for the scopes the flow requests.

## Anthropic Credential Source

The Go SDK path resolves the Anthropic key in memory. It does not write `.env` or `.env.kontext`.

For the hosted provider path, create or use a custom Kontext provider:

```text
displayName: Anthropic
handle: anthropic
authMethod: user_key
```

Attach that provider to the Go SDK demo / managed agent application. `WithCredentials(kx)` uses `ANTHROPIC_API_KEY` when it exists; otherwise, in `CredentialModeProvide`, it exchanges the Kontext session for `resource=anthropic` and passes the returned key to the Anthropic client without printing it.

## Run

```sh
go test ./...
go run ./cmd/custom-loop-demo
```

The default command opens one browser page for Kontext PKCE login, resolves the Anthropic credential from `ANTHROPIC_API_KEY` or the Kontext `anthropic` custom key provider, then runs the Go SDK agent and writes the trace.

For the full hosted provider connect proof:

```sh
go run ./cmd/custom-loop-demo --connect
```

That may open a second OAuth page for the managed `app_<agentID>` client when it needs `gateway:access`, then opens the hosted provider integration page. After viewing the integration page, press Enter in the terminal to run the agent task. If you do not want the pause:

```sh
KONTEXT_DEMO_WAIT_FOR_CONNECT=0 go run ./cmd/custom-loop-demo --connect
```

The demo uses the same native-app callback behavior as `kontext-cli`: it binds `127.0.0.1:0`, picks a free port at runtime, and sends `http://127.0.0.1:<port>/callback` to the OAuth server.

For deterministic local mode without a real Anthropic API call:

```sh
go run ./cmd/custom-loop-demo --fake-anthropic
```

For full engineering/debug output:

```sh
go run ./cmd/custom-loop-demo --verbose
```

For machine-readable Kontext events:

```sh
go run ./cmd/custom-loop-demo --json
```

The default demo prints a clean walkthrough with:

- PKCE login
- managed session creation
- Anthropic credential protection
- Anthropic SDK instrumentation
- `UserPromptSubmit`
- Anthropic request/response observation
- `PreToolUse`
- `PostToolUse`
- local protection/redaction
- trace link

Provider keys are resolved in memory, never written to `.env` or `.env.kontext`, and never printed in terminal output or trace telemetry.

## Target Package Shape

Publish this as one Go module with a core package and an Anthropic adapter package:

```go
import (
    kontext "github.com/kontext-security/kontext-go"
    kxanthropic "github.com/kontext-security/kontext-go/anthropic"
)
```

The core package owns session/auth/telemetry/redaction/hosted connect. The Anthropic adapter package owns `WithCredentials`, `WithRequestTelemetry`, `ObserveTool`, and `WrapTools`.

## Recording Script

Use this narration:

```text
This is the Go-native version of what Kontext does for Claude Code.

With Claude Code, Kontext attaches to Claude Code's native lifecycle hooks.
With a Go SDK agent, there is no external Claude Code hook layer. The equivalent boundary is where the Go process dispatches tools.

So the integration is:
start a Kontext session,
create the Anthropic client with Kontext credentials and request telemetry,
observe the existing tool dispatcher,
and keep the agent loop unchanged.

Here the credential source is resolved without printing the provider key.
Here the request telemetry captures the Claude call.
Here the pre-tool event fires before the local tool runs.
Here the post-tool event fires after the tool returns.
And the payload is redacted locally before telemetry leaves the process.

Once we see the client's actual tool loop, we adapt the wrapper to their exact shape without migrating them to BetaToolRunner.
```
