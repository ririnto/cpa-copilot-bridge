package provider

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ririnto/cpa-copilot-bridge/internal/transport"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"github.com/tidwall/gjson"
)

type errorStreamHost struct {
	chunk   transport.StreamChunk
	readErr error
	closed  bool
}

func (h *errorStreamHost) Do(context.Context, string, transport.Request) (transport.Response, error) {
	return transport.Response{}, errors.New("unexpected request")
}

func (h *errorStreamHost) OpenStream(context.Context, string, transport.Request) (transport.Stream, error) {
	return transport.Stream{}, errors.New("unexpected stream")
}

func (h *errorStreamHost) ReadStream(context.Context, string) (transport.StreamChunk, error) {
	return h.chunk, h.readErr
}

func (h *errorStreamHost) CloseStream(context.Context, string) error {
	h.closed = true
	return nil
}

func (h *errorStreamHost) Emit(context.Context, string, []byte) error { return nil }

func (h *errorStreamHost) CloseOutput(context.Context, string, string) {}

func TestCopilotHeadersUseRecognizedIntegration(t *testing.T) {
	headers := copilotHeaders("test-token", false)

	expected := map[string]string{
		"Copilot-Integration-Id": "vscode-chat",
		"Editor-Plugin-Version":  "copilot-chat/0.35.0",
		"Editor-Version":         "vscode/1.107.0",
		"OpenAI-Intent":          "conversation-edits",
		"User-Agent":             "GitHubCopilotChat/0.35.0",
		"X-GitHub-Api-Version":   "2025-04-01",
	}

	for name, want := range expected {
		if got := headers.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestCountTokensReturnsClaudeInputTokens(t *testing.T) {
	t.Parallel()

	resp, err := (&Service{}).CountTokens(ExecuteRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			SourceFormat:    "claude",
			OriginalRequest: []byte(`{"model":"gpt-5.6-sol","system":"Be concise.","messages":[{"role":"user","content":"hello world"}]}`),
		},
	})
	if err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "input_tokens").Int(); got <= 0 {
		t.Fatalf("input_tokens = %d; response=%s", got, resp.Payload)
	}
}

func TestCollectStreamErrorRedactsSecretsAndCapsBody(t *testing.T) {
	ctx := context.Background()
	githubToken := "ghp_abcdefghijklmnopqrstuvwxyz1234567890"
	copilotToken := "copilot-secret"
	host := &errorStreamHost{readErr: errors.New("connection failed: " + copilotToken + " " + githubToken)}
	service := New(host)
	if _, err := service.collectStreamError(ctx, transport.Stream{ID: "error"}, copilotToken, githubToken); err == nil || strings.Contains(err.Error(), copilotToken) || strings.Contains(err.Error(), githubToken) {
		t.Fatalf("stream read error leaked a credential: %v", err)
	}
	if !host.closed {
		t.Fatal("error stream was not closed")
	}
	host = &errorStreamHost{chunk: transport.StreamChunk{Error: "stream failed: " + copilotToken + " " + githubToken}}
	service = New(host)
	if _, err := service.collectStreamError(ctx, transport.Stream{ID: "chunk-error"}, copilotToken, githubToken); err == nil || strings.Contains(err.Error(), copilotToken) || strings.Contains(err.Error(), githubToken) {
		t.Fatalf("stream chunk error leaked a credential: %v", err)
	}
	host = &errorStreamHost{chunk: transport.StreamChunk{Payload: bytes.Repeat([]byte("x"), (1<<20)+32)}}
	service = New(host)
	body, err := service.collectStreamError(ctx, transport.Stream{ID: "large"}, copilotToken, githubToken)
	if err != nil || len(body) != 1<<20 {
		t.Fatalf("collected error body length = %d, error=%v", len(body), err)
	}
	if !host.closed {
		t.Fatal("bounded error stream was not closed")
	}
}
