package kontextanthropic

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func prettyEventLine(event string, record map[string]any) string {
	switch event {
	case "session.started":
		return fmt.Sprintf(
			"[Kontext] session started: id=%s name=%s service=%s env=%s",
			record["session_id"],
			record["session_name"],
			record["service_name"],
			record["environment"],
		)
	case "prompt.submitted":
		return fmt.Sprintf("[Kontext] UserPromptSubmit: %q", record["prompt"])
	case "provider.credential.resolved":
		return fmt.Sprintf(
			"[Kontext] ProviderCredentialResolved: provider=%s source=%s secret=[REDACTED]",
			record["provider"],
			record["source"],
		)
	case "provider.credential.missing":
		return fmt.Sprintf("[Kontext] ProviderCredentialMissing: provider=%s", record["provider"])
	case "anthropic.request.started":
		return fmt.Sprintf(
			"[Kontext] Anthropic request started: %s %s model=%s",
			record["method"],
			record["path"],
			record["model"],
		)
	case "anthropic.request.completed":
		return fmt.Sprintf(
			"[Kontext] Anthropic request completed: status=%v request_id=%v duration_ms=%v",
			record["status"],
			record["request_id"],
			record["duration_ms"],
		)
	case "tool.pre_use":
		return fmt.Sprintf(
			"[Kontext] PreToolUse: tool=%s input=%s",
			record["tool_name"],
			humanPayload(record["input"]),
		)
	case "tool.post_use":
		return fmt.Sprintf(
			"[Kontext] PostToolUse: tool=%s output=%s duration_ms=%v",
			record["tool_name"],
			humanPayload(record["output"]),
			record["duration_ms"],
		)
	case "session.ended":
		return fmt.Sprintf("[Kontext] session ended: id=%s", record["session_id"])
	default:
		return fmt.Sprintf("[Kontext] %s: %s", event, compactJSON(record))
	}
}

func compactJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func humanPayload(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s=%q", key, fmt.Sprint(typed[key])))
		}
		return strings.Join(parts, " ")
	case []any:
		if len(typed) == 1 {
			if item, ok := typed[0].(map[string]any); ok {
				if text, ok := item["text"]; ok {
					return fmt.Sprintf("%q", fmt.Sprint(text))
				}
			}
		}
		return compactJSON(value)
	default:
		return compactJSON(value)
	}
}
