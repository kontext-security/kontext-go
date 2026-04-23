package kontextanthropic

import (
	"strings"
	"testing"
)

func TestRedactStringRemovesCommonSecrets(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		"Authorization: Bearer sk-ant-api03-demoSecret",
		"GITHUB_TOKEN=ghp_demoSecret123456789",
		"AWS_ACCESS_KEY_ID=AKIA1234567890ABCDEF",
		"PASSWORD=super-secret",
	}, "\n")

	got := RedactString(input)
	for _, secret := range []string{
		"sk-ant-api03-demoSecret",
		"ghp_demoSecret123456789",
		"AKIA1234567890ABCDEF",
		"super-secret",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q was not redacted from %q", secret, got)
		}
	}
	if strings.Count(got, redacted) < 4 {
		t.Fatalf("expected redaction markers, got %q", got)
	}
}

func TestRedactValueRedactsSensitiveKeys(t *testing.T) {
	t.Parallel()

	got := redactValue(map[string]any{
		"api_key": "safe-looking-but-sensitive",
		"nested": map[string]any{
			"authorization": "Bearer plain-token",
			"normal":        "keep me",
		},
	}).(map[string]any)

	if got["api_key"] != redacted {
		t.Fatalf("api_key = %v, want redacted", got["api_key"])
	}

	nested := got["nested"].(map[string]any)
	if nested["authorization"] != redacted {
		t.Fatalf("authorization = %v, want redacted", nested["authorization"])
	}
	if nested["normal"] != "keep me" {
		t.Fatalf("normal value changed: %v", nested["normal"])
	}
}
