package fakeanthropic

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// NewHTTPClient returns a deterministic Anthropic-compatible HTTP client for
// recording and tests. The first message response requests the local list_files
// tool, and the second response returns the final assistant text.
func NewHTTPClient() *http.Client {
	return &http.Client{Transport: &transport{}}
}

type transport struct {
	mu    sync.Mutex
	calls int
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.calls++
	call := t.calls
	t.mu.Unlock()

	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}

	var body string
	switch call {
	case 1:
		body = `{
			"id": "msg_demo_tool_use",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-5",
			"content": [
				{"type": "text", "text": "I'll inspect the demo repo with the local list_files tool."},
				{
					"type": "tool_use",
					"id": "toolu_demo_list_files",
						"name": "list_files",
						"input": {
							"path": ".",
							"note": "recording demo"
						}
					}
			],
			"stop_reason": "tool_use",
			"stop_sequence": null,
			"usage": {"input_tokens": 123, "output_tokens": 45}
		}`
	case 2:
		body = `{
			"id": "msg_demo_final",
			"type": "message",
			"role": "assistant",
				"model": "claude-sonnet-4-5",
				"content": [
						{"type": "text", "text": "This integration demonstrates Kontext credentials, Anthropic request telemetry, and tool-boundary observation inside an existing Go SDK loop."}
				],
			"stop_reason": "end_turn",
			"stop_sequence": null,
			"usage": {"input_tokens": 188, "output_tokens": 30}
		}`
	default:
		return nil, fmt.Errorf("unexpected fake Anthropic call %d", call)
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header: http.Header{
			"Content-Type": {"application/json"},
			"Request-Id":   {fmt.Sprintf("req_demo_%d", call)},
		},
		Body:          io.NopCloser(bytes.NewBufferString(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}
