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

	"github.com/ririnto/cpa-copilot-bridge/internal/compact"
	"github.com/ririnto/cpa-copilot-bridge/internal/translate"
	"github.com/ririnto/cpa-copilot-bridge/internal/transport"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"github.com/tidwall/gjson"
)

type errorStreamHost struct {
	chunk               transport.StreamChunk
	readErr             error
	closed              bool
	closedOutputMessage string
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

func (h *errorStreamHost) CloseOutput(_ context.Context, _ string, message string) {
	h.closedOutputMessage = message
}

type compactionStreamHost struct {
	responseBody  []byte
	emitErrAt     int
	cancelAt      int
	cancel        context.CancelFunc
	mu            sync.Mutex
	requests      []transport.Request
	emitCount     int
	openCount     int
	allowStream   bool
	streamRequest transport.Request
	emitted       chan []byte
	closed        chan string
}

func newCompactionStreamHost(responseBody []byte) *compactionStreamHost {
	return &compactionStreamHost{responseBody: responseBody, emitted: make(chan []byte, 16), closed: make(chan string, 1)}
}

func (h *compactionStreamHost) Do(_ context.Context, _ string, request transport.Request) (transport.Response, error) {
	h.mu.Lock()
	request.Body = append([]byte(nil), request.Body...)
	h.requests = append(h.requests, request)
	h.mu.Unlock()
	if request.Method == http.MethodGet && strings.HasSuffix(request.URL, "/models") {
		return transport.Response{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"data":[{"id":"gpt-5.6-sol","model_picker_enabled":true,"policy":{"state":"enabled"},"capabilities":{"type":"chat"},"supported_endpoints":["/responses"]}]}`)}, nil
	}
	return transport.Response{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: append([]byte(nil), h.responseBody...)}, nil
}

func (h *compactionStreamHost) OpenStream(_ context.Context, _ string, request transport.Request) (transport.Stream, error) {
	h.mu.Lock()
	h.openCount++
	request.Body = append([]byte(nil), request.Body...)
	request.Headers = request.Headers.Clone()
	h.streamRequest = request
	allowStream := h.allowStream
	h.mu.Unlock()
	if !allowStream {
		return transport.Stream{}, errors.New("unexpected upstream stream")
	}
	return transport.Stream{ID: "upstream-stream", StatusCode: http.StatusOK}, nil
}

func (h *compactionStreamHost) ReadStream(context.Context, string) (transport.StreamChunk, error) {
	h.mu.Lock()
	allowStream := h.allowStream
	h.mu.Unlock()
	if !allowStream {
		return transport.StreamChunk{}, errors.New("unexpected upstream stream read")
	}
	return transport.StreamChunk{
		Payload: []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_stream\",\"status\":\"completed\",\"output\":[]}}\n\n"),
		Done:    true,
	}, nil
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

func (h *compactionStreamHost) requestSnapshot() []transport.Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	requests := make([]transport.Request, len(h.requests))
	for index, request := range h.requests {
		requests[index] = request
		requests[index].Body = append([]byte(nil), request.Body...)
		requests[index].Headers = request.Headers.Clone()
	}
	return requests
}

func (h *compactionStreamHost) streamRequestBody() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]byte(nil), h.streamRequest.Body...)
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
	fixture := continuityTestStorage("github-token-for-compaction-test")
	fixture.Type = providerID
	fixture.GitHubLogin = "test-user"
	fixture.ContinuityKeyring.LegacyV1 = &legacyContinuityKey{
		AccountID:             fixture.GitHubUserID,
		AuthID:                "auth-id",
		CredentialFingerprint: tokenFingerprint(fixture.GitHubAccessToken),
		APIBaseURL:            "https://api.example",
	}
	storage, _ := json.Marshal(fixture)
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
			requests := host.requestSnapshot()
			discoveryRequests := make([]transport.Request, 0, 1)
			generationRequests := make([]transport.Request, 0, 1)
			for _, request := range requests {
				switch {
				case request.Method == http.MethodGet && strings.HasSuffix(request.URL, "/models"):
					discoveryRequests = append(discoveryRequests, request)
				case request.Method == http.MethodPost && strings.HasSuffix(request.URL, "/responses"):
					generationRequests = append(generationRequests, request)
				default:
					t.Fatalf("unexpected upstream request method or path: %s %s", request.Method, request.URL)
				}
			}
			if len(discoveryRequests) != 1 || len(generationRequests) != 1 {
				t.Fatalf("upstream discovery/generation requests = %d/%d, want one each", len(discoveryRequests), len(generationRequests))
			}
			generationBody := generationRequests[0].Body
			if bytes.Contains(generationBody, []byte("compaction_trigger")) {
				t.Fatalf("upstream generation request included trigger: %s", generationBody)
			}
			if gjson.GetBytes(generationBody, "tool_choice").String() != "none" {
				t.Fatalf("summary request did not disable tools: %s", generationBody)
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

func TestExecuteStreamExpandsCompactionCapsuleBeforeUpstreamRequest(t *testing.T) {
	t.Parallel()
	host := newCompactionStreamHost(compactionStreamResponse("completed"))
	host.allowStream = true
	service := newCompactionStreamService(t, host)
	const githubToken = "github-token-for-compaction-test"
	model := "gpt-5.6-sol"
	scope, secret := compactionKeyMaterial("auth-id", githubToken, model, translate.EndpointResponses, "https://api.example")
	completed, err := compact.Complete(compactionStreamResponse("completed"), compact.KeyMaterial{Scope: scope, Secret: secret}, nil)
	if err != nil {
		t.Fatalf("create compaction fixture: %v", err)
	}
	capsule := gjson.GetBytes(completed, "output.1.encrypted_content").String()
	if capsule == "" {
		t.Fatal("compaction fixture has no capsule")
	}
	payload := []byte(`{"model":"gpt-5.6-sol","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Continue."}]},{"type":"compaction","encrypted_content":"` + capsule + `"}]}`)
	request := compactionStreamRequest(payload, "")
	if _, err := service.ExecuteStream(context.Background(), request); err != nil {
		t.Fatalf("execute stream with compaction replay: %v", err)
	}
	if _, closeMessage := collectCompactionStreamFrames(t, host); closeMessage != "" {
		t.Fatalf("upstream stream closed with error: %s", closeMessage)
	}
	upstreamBody := host.streamRequestBody()
	if bytes.Contains(upstreamBody, []byte("cpa-copilot-bridge:compaction:")) || bytes.Contains(upstreamBody, []byte(capsule)) {
		t.Fatalf("upstream request retained the bridge capsule: %s", upstreamBody)
	}
	if got := gjson.GetBytes(upstreamBody, "input.1.content.0.text").String(); !strings.Contains(got, "Preserve the active task.") {
		t.Fatalf("upstream request did not contain the expanded summary: %s", upstreamBody)
	}
}

func TestClaudeSafeguardsFailBeforeUpstreamOnTranslatedRoutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		endpoint string
		stream   bool
	}{
		{name: "Chat JSON", endpoint: "/chat/completions"},
		{name: "Responses JSON", endpoint: "/responses"},
		{name: "Chat SSE", endpoint: "/chat/completions", stream: true},
		{name: "Responses SSE", endpoint: "/responses", stream: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			host := &errorStreamHost{}
			service := New(host)
			config := []byte("model_endpoint_overrides:\n  claude-sonnet-5.5: " + test.endpoint + "\n")
			if err := service.Configure(config); err != nil {
				t.Fatalf("configure service: %v", err)
			}
			payload := []byte(`{"safeguards":{"dangerous_tool_use":{"action":"block"}},"messages":[]}`)
			request := ExecuteRequest{ExecutorRequest: pluginapi.ExecutorRequest{
				SourceFormat:    "claude",
				Model:           "claude-sonnet-5.5",
				OriginalRequest: append([]byte(nil), payload...),
				Payload:         append([]byte(nil), payload...),
			}}
			var err error
			if test.stream {
				request.StreamID = "stream-id"
				_, err = service.ExecuteStream(context.Background(), request)
			} else {
				_, err = service.Execute(context.Background(), request)
			}
			var statusErr *StatusError
			if !errors.As(err, &statusErr) || statusErr.Code != "translation_error" || statusErr.HTTPStatus != http.StatusUnprocessableEntity {
				t.Fatalf("request error = %#v, want pre-upstream 422 translation error", err)
			}
			if strings.Contains(err.Error(), "dangerous_tool_use") || !bytes.Equal(request.OriginalRequest, payload) {
				t.Fatalf("request error leaked request data or original history changed: %v", err)
			}
		})
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

func TestResponsesToolIDLimitsRejectBeforeUpstreamAndPreserveValidIDs(t *testing.T) {
	const model = "gpt-5.6-sol"
	const id64 = "iiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiii"
	const callID64 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	for _, stream := range []bool{false, true} {
		for _, test := range []struct {
			name   string
			id     string
			callID string
			valid  bool
		}{
			{name: "item ID over limit", id: id64 + "x", callID: callID64},
			{name: "call ID over limit", id: id64, callID: callID64 + "x"},
			{name: "both IDs at limit", id: id64, callID: callID64, valid: true},
		} {
			t.Run(fmt.Sprintf("stream_%t/%s", stream, test.name), func(t *testing.T) {
				host := newCompactionStreamHost(compactionStreamResponse("completed"))
				host.allowStream = true
				service := newCompactionStreamService(t, host)
				payload, err := json.Marshal(map[string]any{
					"model": model,
					"input": []any{map[string]any{
						"type": "function_call", "id": test.id, "call_id": test.callID, "name": "inspect", "arguments": "{}",
					}},
				})
				if err != nil {
					t.Fatalf("encode request: %v", err)
				}
				request := compactionStreamRequest(payload, "")
				if stream {
					_, err = service.ExecuteStream(context.Background(), request)
				} else {
					_, err = service.Execute(context.Background(), request)
				}
				if !test.valid {
					var statusErr *StatusError
					if !errors.As(err, &statusErr) || statusErr.Code != "responses_tool_id_too_long" || statusErr.HTTPStatus != http.StatusUnprocessableEntity {
						t.Fatalf("request error = %#v, want pre-upstream 422 tool ID error", err)
					}
					if !strings.Contains(statusErr.Message, "input[0]") || !strings.Contains(statusErr.Message, "64 characters") {
						t.Fatalf("tool ID error lacks path or limit: %s", statusErr.Message)
					}
					for _, upstreamRequest := range host.requestSnapshot() {
						if strings.HasSuffix(upstreamRequest.URL, translate.EndpointResponses) {
							t.Fatalf("invalid tool ID was sent upstream: %s", upstreamRequest.URL)
						}
					}
					host.mu.Lock()
					opened := host.openCount
					host.mu.Unlock()
					if opened != 0 || len(host.streamRequestBody()) != 0 {
						t.Fatalf("invalid streaming tool ID opened upstream: opened=%d body=%s", opened, host.streamRequestBody())
					}
					return
				}
				if err != nil {
					t.Fatalf("request with 64-character IDs: %v", err)
				}
				var upstreamBody []byte
				if stream {
					upstreamBody = host.streamRequestBody()
					collectCompactionStreamFrames(t, host)
				} else {
					for _, upstreamRequest := range host.requestSnapshot() {
						if strings.HasSuffix(upstreamRequest.URL, translate.EndpointResponses) {
							upstreamBody = upstreamRequest.Body
						}
					}
				}
				if got := gjson.GetBytes(upstreamBody, "input.0.id").String(); got != id64 {
					t.Fatalf("upstream item ID changed: length=%d body=%s", len(got), upstreamBody)
				}
				if got := gjson.GetBytes(upstreamBody, "input.0.call_id").String(); got != callID64 {
					t.Fatalf("upstream call ID changed: length=%d body=%s", len(got), upstreamBody)
				}
			})
		}
	}
}

func TestResponsesToolIDLimitDoesNotApplyToChatEndpoint(t *testing.T) {
	const model = "gpt-5.6-sol"
	longID := strings.Repeat("i", 65)
	longCallID := strings.Repeat("c", 65)
	chatResponse := []byte(`{"id":"chatcmpl_test","object":"chat.completion","model":"` + model + `","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	host := newCompactionStreamHost(chatResponse)
	service := New(host)
	if err := service.Configure([]byte("model_endpoint_overrides:\n  " + model + ": /chat/completions\n")); err != nil {
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
	payload, err := json.Marshal(map[string]any{
		"model": model,
		"input": []any{
			map[string]any{"type": "function_call", "id": longID, "call_id": longCallID, "name": "inspect", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": longCallID, "output": "done"},
		},
	})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	if _, err := service.Execute(context.Background(), compactionStreamRequest(payload, "")); err != nil {
		t.Fatalf("Chat endpoint rejected long Responses history IDs: %v", err)
	}
	var chatBody []byte
	for _, request := range host.requestSnapshot() {
		if strings.HasSuffix(request.URL, translate.EndpointChatCompletions) {
			chatBody = request.Body
		}
	}
	if len(chatBody) == 0 {
		t.Fatal("Chat request was not sent upstream")
	}
	carrier := gjson.GetBytes(chatBody, "messages.0.tool_calls.0.id").String()
	itemID, callID, ok := translate.DecodeClaudeToolIDs(carrier)
	if !ok || itemID != longID || callID != longCallID {
		t.Fatalf("Chat endpoint changed long history IDs: (%q, %q, %t); body=%s", itemID, callID, ok, chatBody)
	}
	if got := gjson.GetBytes(chatBody, "messages.1.tool_call_id").String(); got != carrier {
		t.Fatalf("Chat tool output ID = %q, want matching carrier %q", got, carrier)
	}
}
