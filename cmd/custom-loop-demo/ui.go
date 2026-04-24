package main

import (
	"fmt"
	"io"
	"sync"
)

type demoUI struct {
	out                io.Writer
	verbose            bool
	json               bool
	mu                 sync.Mutex
	sections           int
	requestStarts      int
	requestCompletions int
}

func newDemoUI(out io.Writer, verbose, json bool) *demoUI {
	if out == nil {
		out = io.Discard
	}
	return &demoUI{out: out, verbose: verbose, json: json}
}

func (ui *demoUI) Header(title, subtitle string) {
	if ui.json {
		return
	}
	fmt.Fprintln(ui.out, "╭──────────────────────────────────────────────╮")
	fmt.Fprintf(ui.out, "│ %-44s │\n", title)
	fmt.Fprintf(ui.out, "│ %-44s │\n", subtitle)
	fmt.Fprintln(ui.out, "╰──────────────────────────────────────────────╯")
	fmt.Fprintln(ui.out)
}

func (ui *demoUI) Success(label, detail string) {
	if ui.json {
		return
	}
	if detail == "" {
		fmt.Fprintf(ui.out, "✓ %s\n", label)
		return
	}
	fmt.Fprintf(ui.out, "✓ %-28s %s\n", label, detail)
}

func (ui *demoUI) Section(label string) {
	if ui.json {
		return
	}
	if ui.sections > 0 {
		fmt.Fprintln(ui.out)
	}
	ui.sections++
	fmt.Fprintln(ui.out, label)
}

func (ui *demoUI) Text(text string) {
	if ui.json {
		return
	}
	fmt.Fprintf(ui.out, "  %s\n", text)
}

func (ui *demoUI) Event(label, detail string) {
	if ui.json {
		return
	}
	if detail == "" {
		fmt.Fprintf(ui.out, "  → %s\n", label)
		return
	}
	fmt.Fprintf(ui.out, "  → %-26s %s\n", label, detail)
}

func (ui *demoUI) Incoming(label, detail string) {
	if ui.json {
		return
	}
	if detail == "" {
		fmt.Fprintf(ui.out, "  ← %s\n", label)
		return
	}
	fmt.Fprintf(ui.out, "  ← %-26s %s\n", label, detail)
}

func (ui *demoUI) Warning(label, detail string) {
	if ui.json {
		return
	}
	if detail == "" {
		fmt.Fprintf(ui.out, "! %s\n", label)
		return
	}
	fmt.Fprintf(ui.out, "! %-28s %s\n", label, detail)
}

func (ui *demoUI) DebugWriter() io.Writer {
	if ui.verbose && !ui.json {
		return ui.out
	}
	return io.Discard
}

func (ui *demoUI) OnKontextEvent(eventName string, record map[string]any) {
	if ui.json || ui.verbose {
		return
	}

	ui.mu.Lock()
	defer ui.mu.Unlock()

	switch eventName {
	case "session.started":
		ui.Success("Session started", fmt.Sprintf("%s · %s", recordString(record, "service_name"), recordString(record, "environment")))
	case "provider.credential.missing":
		ui.Warning("Anthropic credential", "missing")
	case "anthropic.request.started":
		ui.requestStarts++
		switch ui.requestStarts {
		case 1:
			ui.Event("Claude request #1", "waiting for tool decision")
		case 2:
			ui.Event("Claude request #2", "sending tool_result")
		default:
			ui.Event(fmt.Sprintf("Claude request #%d", ui.requestStarts), "continuing agent loop")
		}
	case "anthropic.request.completed":
		ui.requestCompletions++
		if ui.requestCompletions >= 2 {
			ui.Incoming("Claude final response", durationDetail(record))
		}
	case "tool.pre_use":
		ui.Incoming("Claude tool_use", toolCallDetail(record))
		ui.Event("Tool start", recordString(record, "tool_name"))
	case "tool.post_use":
		ui.Incoming("Tool result", "output protected")
	}
}

func recordString(record map[string]any, key string) string {
	value, _ := record[key].(string)
	return value
}

func durationDetail(record map[string]any) string {
	value, ok := record["duration_ms"]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%vms", value)
}

func toolCallDetail(record map[string]any) string {
	tool := recordString(record, "tool_name")
	input, _ := record["input"].(map[string]any)
	path := "."
	if inputPath, ok := input["path"]; ok {
		path = fmt.Sprint(inputPath)
	}
	if tool == "" {
		return fmt.Sprintf("path=%q", path)
	}
	return fmt.Sprintf("%s(path=%q)", tool, path)
}
