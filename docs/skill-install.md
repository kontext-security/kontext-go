# Kontext Go Integrator Skill

The Go package and the skill are different pieces:

- The Go package is runtime code compiled into the customer's agent.
- The skill is a coding-agent instruction bundle that patches the customer's repo to use the Go package.

The Go module has two public packages:

- `github.com/kontext-security/kontext-go` for Kontext session, credentials, and prompt tracking.
- `github.com/kontext-security/kontext-go/anthropic` for Anthropic SDK adapters.

Install the runtime package in a Go repo:

```sh
go get github.com/kontext-security/kontext-go@v0.1.3
```

Preferred skill install:

```sh
npx skills add kontext-security/skills
```

Or install only the Go integrator skill:

```sh
curl -fsSL https://raw.githubusercontent.com/kontext-security/kontext-go/main/scripts/install-skill.sh | sh
```

Or download the zip directly:

```sh
curl -L -o kontext-go-integrator.zip \
  https://github.com/kontext-security/kontext-go/releases/download/v0.1.3/kontext-go-integrator.zip
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
