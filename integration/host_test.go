package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixture struct {
	mu       sync.Mutex
	requests []map[string]any
	paths    []string
	canceled chan struct{}
}

func TestNativeHostProtocolRoundTrips(t *testing.T) {
	binary := os.Getenv("CPA_BINARY")
	if binary == "" {
		t.Skip("set CPA_BINARY to a built CLIProxyAPI v8 server for native host integration")
	}
	state := &fixture{canceled: make(chan struct{})}
	upstream := httptest.NewServer(state)
	t.Cleanup(upstream.Close)
	base := startProxy(t, binary, upstream.URL)
	longID := "opaque/+" + strings.Repeat("x", 416)
	items := []any{
		map[string]any{"type": "reasoning", "id": strings.Repeat("reasoning/+", 13), "summary": []any{}, "encrypted_content": "copilot-opaque/+not-fernet"},
		map[string]any{"type": "function_call", "id": longID, "call_id": "call/+", "name": "inspect", "arguments": "{}"},
		map[string]any{"type": "function_call_output", "call_id": "call/+", "output": "ok"},
	}
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("ResponsesStream%v", stream), func(t *testing.T) {
			request := map[string]any{"model": "bridge-responses", "stream": stream, "input": items, "store": false, "previous_response_id": "provider_previous/+", "temperature": 0.6, "prompt_cache_key": "caller-key"}
			response := callProxy(t, base+"/v1/responses", request)
			state.mu.Lock()
			captured := state.requests[len(state.requests)-1]
			state.mu.Unlock()
			assertEqualJSON(t, captured["input"], items)
			if captured["previous_response_id"] != request["previous_response_id"] || captured["prompt_cache_key"] != "caller-key" || captured["temperature"] != 0.6 {
				t.Fatalf("native request state changed: %+v", captured)
			}
			if !bytes.Contains(response, []byte("copilot-opaque/+not-fernet")) || !bytes.Contains(response, []byte(longID)) {
				t.Fatalf("native output lost opaque state: %s", response)
			}
			if stream && bytes.Count(response, []byte(`"type":"response.completed"`)) != 1 {
				t.Fatalf("expected one terminal event: %s", response)
			}
		})
	}
	t.Run("ResponsesClientRemovedItemIDs", func(t *testing.T) {
		withoutIDs := []any{
			map[string]any{"type": "reasoning", "summary": []any{}, "encrypted_content": "copilot-opaque/+not-fernet"},
			map[string]any{"type": "function_call", "call_id": "call/+", "name": "inspect", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "call/+", "output": "ok"},
		}
		callProxy(t, base+"/v1/responses", map[string]any{"model": "bridge-responses", "input": withoutIDs})
		state.mu.Lock()
		captured := state.requests[len(state.requests)-1]
		state.mu.Unlock()
		assertEqualJSON(t, captured["input"], items)
	})
	t.Run("ClaudeNativeSignedThinking", func(t *testing.T) {
		messages := []any{map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": "", "signature": "claude-opaque/+"},
			map[string]any{"type": "redacted_thinking", "data": "redacted/+"},
			map[string]any{"type": "tool_use", "id": "tool_safe", "name": "inspect", "input": map[string]any{}},
		}}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "tool_safe", "content": "ok"}}}}
		response := callProxy(t, base+"/v1/messages", map[string]any{"model": "bridge-messages", "max_tokens": 128, "messages": messages})
		state.mu.Lock()
		captured := state.requests[len(state.requests)-1]
		path := state.paths[len(state.paths)-1]
		state.mu.Unlock()
		if path != "/v1/messages" {
			t.Fatalf("native Claude request used %s", path)
		}
		assertEqualJSON(t, captured["messages"], messages)
		if !bytes.Contains(response, []byte("claude-opaque/+")) {
			t.Fatalf("Claude signature missing: %s", response)
		}
	})
	t.Run("ChatNative", func(t *testing.T) {
		messages := []any{map[string]any{"role": "user", "content": "hello"}}
		response := callProxy(t, base+"/v1/chat/completions", map[string]any{"model": "bridge-chat", "messages": messages, "prompt_cache_key": "explicit-chat-key"})
		state.mu.Lock()
		captured := state.requests[len(state.requests)-1]
		path := state.paths[len(state.paths)-1]
		state.mu.Unlock()
		if path != "/chat/completions" || captured["prompt_cache_key"] != "explicit-chat-key" {
			t.Fatalf("chat route or cache key changed: %s %+v", path, captured)
		}
		assertEqualJSON(t, captured["messages"], messages)
		if !bytes.Contains(response, []byte("chat.completion")) {
			t.Fatalf("chat response malformed: %s", response)
		}
	})
	t.Run("ClientCancellationClosesUpstream", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		body := []byte(`{"model":"bridge-responses","stream":true,"input":"Wait for cancellation","fixture_cancel":true}`)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/responses", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer fixture-client-key")
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("stream did not start: status=%d", response.StatusCode)
		}
		if _, err := io.ReadFull(response.Body, make([]byte, 1)); err != nil {
			t.Fatalf("stream produced no frame before cancellation: %v", err)
		}
		cancel()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		select {
		case <-state.canceled:
		case <-deadline.C:
			t.Fatal("native plugin did not cancel the upstream HTTP stream")
		}
	})
	t.Run("ResponsesCompactionTriggerRoundTrip", func(t *testing.T) {
		history := []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Remember the active goal: finish the native host bridge."}}}}
		input := append(append([]any(nil), history...), map[string]any{"type": "compaction_trigger"})
		response := callProxy(t, base+"/v1/responses", map[string]any{"model": "bridge-responses", "input": input})
		captured, path := lastUpstreamRequest(t, state)
		assertCompactionRequest(t, captured, path)
		capsule := assertSingleCompaction(t, response)
		assertCompactionReplay(t, base, state, capsule, "Continue the bridge task.")
	})
	t.Run("ResponsesCompactRouteRoundTrip", func(t *testing.T) {
		request := map[string]any{
			"model": "bridge-responses",
			"input": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Summarize the native host bridge task."}}}},
		}
		response := callProxy(t, base+"/v1/responses/compact", request)
		captured, path := lastUpstreamRequest(t, state)
		assertCompactionRequest(t, captured, path)
		capsule := assertSingleCompaction(t, response)
		assertCompactionReplay(t, base, state, capsule, "Continue after the compact route.")
	})
}

func (f *fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/copilot_internal/v2/token":
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "fixture-copilot-token", "expires_at": time.Now().Add(time.Hour).Unix(), "endpoints": map[string]string{"api": "http://" + r.Host}})
	case "/models":
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{
			map[string]any{"id": "bridge-responses", "supported_endpoints": []string{"/responses"}, "capabilities": map[string]any{"type": "chat", "supports": map[string]bool{"streaming": true, "tool_calls": true}}},
			map[string]any{"id": "bridge-messages", "supported_endpoints": []string{"/v1/messages", "/chat/completions"}, "capabilities": map[string]any{"type": "chat"}},
			map[string]any{"id": "bridge-chat", "supported_endpoints": []string{"/chat/completions"}, "capabilities": map[string]any{"type": "chat"}},
		}})
	case "/responses", "/v1/messages", "/chat/completions":
		if r.Header.Get("Authorization") != "Bearer fixture-copilot-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, request)
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		if request["fixture_cancel"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			writeEvent(w, map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_cancel", "status": "in_progress", "output": []any{}}})
			<-r.Context().Done()
			close(f.canceled)
			return
		}
		if r.URL.Path == "/v1/messages" {
			_, _ = io.WriteString(w, `{"id":"msg_fixture","type":"message","role":"assistant","model":"bridge-messages","content":[{"type":"thinking","thinking":"","signature":"claude-opaque/+"},{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		if r.URL.Path == "/chat/completions" {
			_, _ = io.WriteString(w, `{"id":"chat_fixture","object":"chat.completion","model":"bridge-chat","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			return
		}
		output := []any{
			map[string]any{"type": "reasoning", "id": strings.Repeat("reasoning/+", 13), "summary": []any{}, "encrypted_content": "copilot-opaque/+not-fernet"},
			map[string]any{"type": "function_call", "id": "opaque/+" + strings.Repeat("x", 416), "call_id": "call/+", "name": "inspect", "arguments": "{}", "status": "completed"},
		}
		if request["tool_choice"] == "none" {
			output = []any{map[string]any{"type": "message", "id": "msg_summary", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "The active goal is to finish the native host bridge. The next step is integration."}}}}
		}
		response := map[string]any{"id": "resp_fixture", "object": "response", "status": "completed", "model": "bridge-responses", "output": output, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}
		if request["stream"] != true {
			_ = json.NewEncoder(w).Encode(response)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for i, item := range output {
			writeEvent(w, map[string]any{"type": "response.output_item.added", "output_index": i, "item": item})
			writeEvent(w, map[string]any{"type": "response.output_item.done", "output_index": i, "item": item})
		}
		writeEvent(w, map[string]any{"type": "response.completed", "response": response})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func writeEvent(w http.ResponseWriter, event any) {
	body, _ := json.Marshal(event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", body)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func startProxy(t *testing.T, binary, upstream string) string {
	t.Helper()
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	pluginDir := filepath.Join(root, "plugins", runtime.GOOS, runtime.GOARCH)
	if err := os.MkdirAll(pluginDir, 0700); err != nil {
		t.Fatal(err)
	}
	ext := ".so"
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}
	if runtime.GOOS == "windows" {
		ext = ".dll"
	}
	artifact, err := os.ReadFile(filepath.Join("..", "build", "plugins", runtime.GOOS, runtime.GOARCH, "cpa-copilot-bridge"+ext))
	if err != nil {
		t.Fatalf("run go tool task build before host integration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "cpa-copilot-bridge"+ext), artifact, 0600); err != nil {
		t.Fatal(err)
	}
	authDir := filepath.Join(root, "auths")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "fixture.json"), []byte(`{"type":"copilot-bridge","github_access_token":"fixture-github-token","github_login":"fixture","prefix":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("config-version: 8\nserver:\n  host: 127.0.0.1\n  port: %d\nmanagement:\n  disable-control-panel: true\naccess:\n  api-keys: [fixture-client-key]\noauth:\n  auth-dir: %q\nplugins:\n  enabled: true\n  dir: %q\n  configs:\n    cpa-copilot-bridge:\n      enabled: true\n      allow_insecure_base_urls: true\n      compaction_models: [bridge-responses]\n      github_base_url: %q\n      github_api_url: %q\n      copilot_api_url: %q\n", port, authDir, filepath.Join(root, "plugins"), upstream, upstream, upstream)
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "host.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, binary, "--config", configPath, "--local-model")
	command.Dir = root
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = command.Wait(); _ = logFile.Close() })
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			log, _ := os.ReadFile(logPath)
			t.Fatalf("native host failed to load plugin: %s", log)
		case <-ticker.C:
			request, _ := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
			request.Header.Set("Authorization", "Bearer fixture-client-key")
			response, err := http.DefaultClient.Do(request)
			if err == nil {
				body, _ := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if response.StatusCode == 200 && bytes.Contains(body, []byte("bridge-responses")) {
					return base
				}
			}
		}
	}
}

func callProxy(t *testing.T, url string, payload any) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer fixture-client-key")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Session-Id", "fixture-conversation")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	out, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("native request failed: status=%d body=%s err=%v", response.StatusCode, out, err)
	}
	return out
}

func lastUpstreamRequest(t *testing.T, state *fixture) (map[string]any, string) {
	t.Helper()
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.requests) == 0 || len(state.requests) != len(state.paths) {
		t.Fatalf("fixture captured mismatched request state: requests=%d paths=%d", len(state.requests), len(state.paths))
	}
	last := len(state.requests) - 1
	return state.requests[last], state.paths[last]
}

func assertCompactionRequest(t *testing.T, request map[string]any, path string) {
	t.Helper()
	if path != "/responses" {
		t.Fatalf("compaction request used upstream path %q", path)
	}
	if request["tool_choice"] != "none" {
		t.Fatalf("compaction request tool_choice = %v, want none", request["tool_choice"])
	}
	if _, exists := request["tools"]; exists {
		t.Fatalf("compaction request retained tools: %+v", request)
	}
	input, err := json.Marshal(request["input"])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(input, []byte("compaction_trigger")) {
		t.Fatalf("raw compaction trigger reached Copilot: %s", input)
	}
	if !bytes.Contains(input, []byte("transcript data")) {
		t.Fatalf("summary instruction missing from Copilot request: %s", input)
	}
}

func assertSingleCompaction(t *testing.T, response []byte) string {
	t.Helper()
	var result struct {
		Status string `json:"status"`
		Output []struct {
			Type             string `json:"type"`
			EncryptedContent string `json:"encrypted_content"`
			Content          []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(response, &result); err != nil {
		t.Fatalf("decode compaction response: %v: %s", err, response)
	}
	if result.Status != "completed" {
		t.Fatalf("compaction response status = %q, want completed", result.Status)
	}
	count := 0
	var capsule string
	var summary string
	for _, item := range result.Output {
		if item.Type == "compaction" {
			count++
			capsule = item.EncryptedContent
		}
		for _, part := range item.Content {
			summary += part.Text
		}
	}
	if count != 1 || capsule == "" {
		t.Fatalf("compaction item count = %d, capsule present=%v: %s", count, capsule != "", response)
	}
	if !strings.Contains(summary, "active goal is to finish the native host bridge") {
		t.Fatalf("ordinary summary output missing or changed: %s", response)
	}
	return capsule
}

func assertCompactionReplay(t *testing.T, base string, state *fixture, capsule, continuation string) {
	t.Helper()
	request := map[string]any{"model": "bridge-responses", "input": []any{
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": continuation}}},
		map[string]any{"type": "compaction", "encrypted_content": capsule},
	}}
	callProxy(t, base+"/v1/responses", request)
	captured, path := lastUpstreamRequest(t, state)
	if path != "/responses" {
		t.Fatalf("replay request used upstream path %q", path)
	}
	input, err := json.Marshal(captured["input"])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(input, []byte(capsule)) || bytes.Contains(input, []byte("cpa-copilot-bridge:compaction:")) || bytes.Contains(input, []byte(`"type":"compaction"`)) {
		t.Fatalf("replay request retained opaque capsule: %s", input)
	}
	if !bytes.Contains(input, []byte("The active goal is to finish the native host bridge")) || !bytes.Contains(input, []byte(continuation)) {
		t.Fatalf("replay request omitted prior summary or continuation: %s", input)
	}
}

func assertEqualJSON(t *testing.T, actual, expected any) {
	t.Helper()
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(expected)
	if !bytes.Equal(a, b) {
		t.Fatalf("protocol payload changed: actual=%s expected=%s", a, b)
	}
}
