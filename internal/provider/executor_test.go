package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

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

type compactionStreamHost struct {
	responseBody []byte
	emitErrAt    int
	cancelAt     int
	cancel       context.CancelFunc
	mu           sync.Mutex
	requests     []transport.Request
	emitCount    int
	openCount    int
	emitted      chan []byte
	closed       chan string
}

func newCompactionStreamHost(responseBody []byte) *compactionStreamHost {
	return &compactionStreamHost{responseBody: responseBody, emitted: make(chan []byte, 16), closed: make(chan string, 1)}
}

func (h *compactionStreamHost) Do(_ context.Context, _ string, request transport.Request) (transport.Response, error) {
	h.mu.Lock()
	request.Body = append([]byte(nil), request.Body...)
	h.requests = append(h.requests, request)
	h.mu.Unlock()
	return transport.Response{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: append([]byte(nil), h.responseBody...)}, nil
}

func (h *compactionStreamHost) OpenStream(context.Context, string, transport.Request) (transport.Stream, error) {
	h.mu.Lock()
	h.openCount++
	h.mu.Unlock()
	return transport.Stream{}, errors.New("unexpected upstream stream")
}

func (h *compactionStreamHost) ReadStream(context.Context, string) (transport.StreamChunk, error) {
	return transport.StreamChunk{}, errors.New("unexpected upstream stream read")
}

func (h *compactionStreamHost) CloseStream(context.Context, string) error { return nil }

func (h *compactionStreamHost) Emit(_ context.Context, _ string, frame []byte) error {
	h.mu.Lock()
	h.emitCount++
	count := h.emitCount
	emitErrAt := h.emitErrAt
	cancelAt := h.cancelAt
	cancel := h.cancel
	h.mu.Unlock()
	if count == emitErrAt {
		return errors.New("synthetic emit failure")
	}
	h.emitted <- append([]byte(nil), frame...)
	if count == cancelAt && cancel != nil {
		cancel()
	}
	return nil
}

func (h *compactionStreamHost) CloseOutput(_ context.Context, _ string, message string) {
	h.closed <- message
}

func (h *compactionStreamHost) requestBodies() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	bodies := make([][]byte, len(h.requests))
	for index, request := range h.requests {
		bodies[index] = append([]byte(nil), request.Body...)
	}
	return bodies
}

func newCompactionStreamService(t *testing.T, host *compactionStreamHost) *Service {
	t.Helper()
	service := New(host)
	if err := service.Configure([]byte("compaction_models:\n  - gpt-5.6-sol\nmodel_endpoint_overrides:\n  gpt-5.6-sol: /responses\n")); err != nil {
		t.Fatalf("configure service: %v", err)
	}
	_, generation := service.configSnapshot()
	githubToken := "github-token-for-compaction-test"
	service.tokenEntries["auth-id"] = copilotTokenEntry{
		Token:            "copilot-token",
		APIBaseURL:       "https://api.example",
		ExpiresAt:        time.Now().Add(time.Hour),
		Fingerprint:      tokenFingerprint(githubToken),
		ConfigGeneration: generation,
	}
	return service
}

func compactionStreamRequest(payload []byte, alt string) ExecuteRequest {
	storage, _ := json.Marshal(authStorage{Type: providerID, GitHubAccessToken: "github-token-for-compaction-test", GitHubLogin: "test-user"})
	return ExecuteRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			AuthID:          "auth-id",
			SourceFormat:    "openai-response",
			Model:           "gpt-5.6-sol",
			OriginalRequest: append([]byte(nil), payload...),
			Payload:         append([]byte(nil), payload...),
			StorageJSON:     storage,
			Alt:             alt,
		},
		StreamID: "stream-id",
	}
}

func collectCompactionStreamFrames(t *testing.T, host *compactionStreamHost) ([][]byte, string) {
	t.Helper()
	frames := make([][]byte, 0, 16)
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case frame := <-host.emitted:
			frames = append(frames, frame)
		case message := <-host.closed:
			for {
				select {
				case frame := <-host.emitted:
					frames = append(frames, frame)
				default:
					return frames, message
				}
			}
		case <-timer.C:
			t.Fatalf("stream did not close after %d frames", len(frames))
		}
	}
}

func compactionStreamResponse(status string) []byte {
	return []byte(fmt.Sprintf(`{"id":"resp_compact","object":"response","status":%q,"model":"gpt-5.6-sol","metadata":{"origin":"test"},"output":[{"id":"msg_summary","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Preserve the active task."}]}],"usage":{"input_tokens":17,"output_tokens":5,"total_tokens":22}}`, status))
}

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

func TestExecuteStreamBuffersCompactionAndEmitsResponsesSSE(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload string
		alt     string
	}{
		{name: "Codex V2 trigger", payload: `{"model":"gpt-5.6-sol","stream":true,"input":[{"type":"compaction_trigger"}]}`},
		{name: "V1 compact route", payload: `{"model":"gpt-5.6-sol","stream":true,"input":[{"type":"message","role":"user","content":"earlier history"}]}`, alt: "responses/compact"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			host := newCompactionStreamHost(compactionStreamResponse("completed"))
			service := newCompactionStreamService(t, host)
			headers, err := service.ExecuteStream(context.Background(), compactionStreamRequest([]byte(test.payload), test.alt))
			if err != nil {
				t.Fatalf("execute stream: %v", err)
			}
			if headers.Get("Content-Type") != "text/event-stream" || headers.Get("Cache-Control") != "no-cache" {
				t.Fatalf("stream headers = %#v", headers)
			}
			frames, closeMessage := collectCompactionStreamFrames(t, host)
			if len(frames) != 7 {
				t.Fatalf("got %d stream frames, want 7", len(frames))
			}
			if closeMessage != "" {
				t.Fatalf("successful stream closed with error %q", closeMessage)
			}
			wantEvents := []string{"response.created", "response.in_progress", "response.output_item.added", "response.output_item.done", "response.output_item.added", "response.output_item.done", "response.completed"}
			var completed map[string]json.RawMessage
			for index, frame := range frames {
				event, data := parseSSEFrame(frame)
				if event != wantEvents[index] || gjson.Get(data, "type").String() != event {
					t.Fatalf("frame %d event/type = %q/%q, want %q: %s", index, event, gjson.Get(data, "type").String(), wantEvents[index], data)
				}
				if got := gjson.Get(data, "sequence_number").Int(); got != int64(index) {
					t.Fatalf("frame %d sequence_number = %d", index, got)
				}
				if index == 3 && gjson.Get(data, "item.id").String() != "msg_summary" {
					t.Fatalf("first output item = %s", data)
				}
				if index == 5 && (gjson.Get(data, "item.type").String() != "compaction" || !strings.HasPrefix(gjson.Get(data, "item.encrypted_content").String(), "cpa-copilot-bridge:compaction:v1:")) {
					t.Fatalf("compaction output item = %s", data)
				}
				if index == 6 {
					if err := json.Unmarshal([]byte(data), &completed); err != nil {
						t.Fatalf("decode terminal frame: %v", err)
					}
				}
			}
			terminal := string(completed["response"])
			if gjson.Get(terminal, "id").String() != "resp_compact" || gjson.Get(terminal, "output.0.id").String() != "msg_summary" || gjson.Get(terminal, "output.1.type").String() != "compaction" {
				t.Fatalf("terminal output lost identity or order: %s", terminal)
			}
			if gjson.Get(terminal, "usage.total_tokens").Int() != 22 || gjson.Get(terminal, "metadata.origin").String() != "test" {
				t.Fatalf("terminal usage or metadata changed: %s", terminal)
			}
			requestBodies := host.requestBodies()
			if len(requestBodies) != 1 || bytes.Contains(requestBodies[0], []byte("compaction_trigger")) {
				t.Fatalf("upstream request included trigger or wrong count: %q", requestBodies)
			}
			if gjson.GetBytes(requestBodies[0], "tool_choice").String() != "none" {
				t.Fatalf("summary request did not disable tools: %s", requestBodies[0])
			}
			host.mu.Lock()
			openCount := host.openCount
			host.mu.Unlock()
			if openCount != 0 {
				t.Fatalf("compaction used upstream stream transport %d times", openCount)
			}
		})
	}
}

func TestExecuteStreamCompactionFailsBeforeEmittingSuccess(t *testing.T) {
	t.Parallel()
	host := newCompactionStreamHost(compactionStreamResponse("incomplete"))
	service := newCompactionStreamService(t, host)
	request := compactionStreamRequest([]byte(`{"model":"gpt-5.6-sol","stream":true,"input":[{"type":"compaction_trigger"}]}`), "")
	if _, err := service.ExecuteStream(context.Background(), request); err == nil {
		t.Fatal("incomplete summary response was accepted")
	}
	if len(host.emitted) != 0 || len(host.closed) != 0 {
		t.Fatal("failed compaction emitted or closed a successful stream")
	}
}

func TestExecuteStreamCompactionStopsWithoutCompletedEvent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		emitErrAt int
		cancelAt  int
	}{
		{name: "emit failure", emitErrAt: 3},
		{name: "cancellation", cancelAt: 1},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			host := newCompactionStreamHost(compactionStreamResponse("completed"))
			host.emitErrAt = test.emitErrAt
			host.cancelAt = test.cancelAt
			host.cancel = cancel
			service := newCompactionStreamService(t, host)
			request := compactionStreamRequest([]byte(`{"model":"gpt-5.6-sol","stream":true,"input":[{"type":"compaction_trigger"}]}`), "")
			if _, err := service.ExecuteStream(ctx, request); err != nil {
				t.Fatalf("execute stream: %v", err)
			}
			frames, closeMessage := collectCompactionStreamFrames(t, host)
			if closeMessage == "" {
				t.Fatal("failed stream reported successful closure")
			}
			for _, frame := range frames {
				event, _ := parseSSEFrame(frame)
				if event == "response.completed" {
					t.Fatal("failed or canceled stream emitted response.completed")
				}
			}
		})
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
