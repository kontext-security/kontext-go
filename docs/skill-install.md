# Kontext Go Integrator Skill

The Go package and the skill are different pieces:

- The Go package is runtime code compiled into the customer's agent.
- The skill is a coding-agent instruction bundle that patches the customer's repo to use the Go package.

The Go module has two public packages:

- `github.com/kontext-security/kontext-go` for Kontext session, credentials, and prompt tracking.
- `github.com/kontext-security/kontext-go/anthropic` for Anthropic SDK adapters.

In short: one installable Go module, two import paths, one separate skills repo.

Install the runtime package in a Go repo:

```sh
go get github.com/kontext-security/kontext-go@v0.1.4
```

Preferred skill install:

```sh
npx skills add kontext-security/skills
```

Then ask the coding agent:

```text
Use the kontext-go-integrator skill to integrate Kontext into this Anthropic Go SDK agent. Preserve the existing loop. Add github.com/kontext-security/kontext-go@v0.1.4, add Kontext session start/end, add WithCredentials(kx) and WithRequestTelemetry(kx), add TrackPrompt if obvious, and wrap the existing tool dispatch boundary with ObserveTool. Then run gofmt, go mod tidy, and go test ./...
```

Or install only the Go integrator skill:

```sh
curl -fsSL https://raw.githubusercontent.com/kontext-security/kontext-go/main/scripts/install-skill.sh | sh
```

Or download the zip directly:

```sh
curl -L -o kontext-go-integrator.zip \
  https://github.com/kontext-security/kontext-go/releases/download/v0.1.4/kontext-go-integrator.zip
```

In Codex Desktop, import `kontext-go-integrator.zip`, or unzip it into:

```text
~/.codex/skills/kontext-go-integrator
```

Then open the customer's Go repo and ask:

```text
Use the kontext-go-integrator skill to integrate Kontext into this Anthropic Go SDK agent.
```

The skill should preserve the existing agent loop, add Kontext credentials and request telemetry at `anthropic.NewClient`, track the prompt when obvious, and wrap the existing tool execution call with `kxanthropic.ObserveTool`.

Fallbacks for live setup:

- Skill zip: `https://github.com/kontext-security/kontext-go/releases/download/v0.1.4/kontext-go-integrator.zip`
- Runtime install: `go get github.com/kontext-security/kontext-go@v0.1.4`
- Demo connect: `go run ./cmd/custom-loop-demo --connect`
- Live proof: `env -u ANTHROPIC_API_KEY go run ./cmd/custom-loop-demo`
