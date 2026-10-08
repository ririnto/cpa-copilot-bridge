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
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestNativeHostToolAndSystemCompatibility(t *testing.T) {
	binary := os.Getenv("CPA_BINARY")
	if binary == "" {
		t.Skip("set CPA_BINARY to the prepared CLIProxyAPI v8 server for native host integration")
	}
	state := newNativeFixture(t)
	upstream := httptest.NewServer(state)
	t.Cleanup(upstream.Close)
	base := startProxy(t, binary, upstream.URL)
	t.Run("ExclusionHeader", func(t *testing.T) {
		body, err := json.Marshal(map[string]any{"model": "claude-sonnet-5.5", "input": "Say hello.", "tools": []any{map[string]any{"type": "image_generation"}}})
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, base+"/v1/responses", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer fixture-client-key")
		request.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 10 * time.Second}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if _, err := io.Copy(io.Discard, response.Body); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("X-Copilot-Excluded-Native-Tools"), "image_generation;reason=unrepresentable_by_selected_endpoint") {
			t.Fatalf("degradation disclosure was not forwarded: status=%d headers=%v", response.StatusCode, response.Header)
		}
	})
	t.Run("ClaudeSafeguardsClientError", func(t *testing.T) {
		const opaqueContext = "OPAQUE-CLASSIFIER-CONTEXT-SECRET"
		for _, test := range []struct {
			name   string
			stream bool
		}{
			{name: "JSON"},
			{name: "SSE", stream: true},
		} {
			test := test
			t.Run(test.name, func(t *testing.T) {
				before := upstreamRequestCount(state)
				request := map[string]any{
					"model":      "claude-sonnet-5.5",
					"stream":     test.stream,
					"max_tokens": 64,
					"messages":   []any{map[string]any{"role": "user", "content": "Say hello."}},
					"safeguards": []any{map[string]any{"type": "dangerous_tool_use", "classifier_context": opaqueContext}},
				}
				status, response := postProxyWithSession(t, base+"/v1/messages", request, "claude-safeguards-client-error-"+test.name)
				if status != http.StatusBadRequest || !strings.Contains(strings.ToLower(string(response)), "safeguards") {
					t.Fatalf("Claude safeguard response = %d %s, want static safeguards 400", status, response)
				}
				if bytes.Contains(response, []byte(opaqueContext)) {
					t.Fatalf("Claude safeguard response leaked classifier context: %s", response)
				}
				if after := upstreamRequestCount(state); after != before {
					t.Fatalf("rejected Claude safeguard dispatched %d upstream inference requests", after-before)
				}
			})
		}
	})
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("Stream%v", stream), func(t *testing.T) {
			for _, model := range []string{"claude-sonnet-5.5", "gemini-3.8-flash"} {
				before := upstreamRequestCount(state)
				request := map[string]any{"model": model, "stream": stream, "input": "Draw a circle.", "tools": []any{map[string]any{"type": "image_generation"}}}
				status, response := postProxyWithSession(t, base+"/v1/responses", request, fmt.Sprintf("native-image-unsupported-%s-%t", model, stream))
				if status != http.StatusOK {
					t.Fatalf("unsupported image tool returned %d: %s", status, response)
				}
				if after := upstreamRequestCount(state); after != before+1 {
					t.Fatalf("degraded request dispatched %d inference requests, want exactly one", after-before)
				}
				captured, _ := lastUpstreamRequest(t, state)
				if tools, exists := captured["tools"]; exists {
					array, ok := tools.([]any)
					if !ok || len(array) != 0 {
						t.Fatalf("unrepresentable native image tool reached upstream: %+v", captured)
					}
				}
			}
			before := upstreamRequestCount(state)
			nested := map[string]any{"model": "claude-sonnet-5.5", "stream": stream, "input": []any{
				map[string]any{"type": "message", "role": "user", "content": "Say hello."},
				map[string]any{"type": "additional_tools", "tools": []any{
					map[string]any{"type": "image_generation"},
					map[string]any{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}},
				}},
			}}
			status, response := postProxyWithSession(t, base+"/v1/responses", nested, fmt.Sprintf("native-nested-tools-%t", stream))
			if status != http.StatusOK {
				t.Fatalf("nested tool degradation returned %d: %s", status, response)
			}
			if after := upstreamRequestCount(state); after != before+1 {
				t.Fatalf("nested degraded request dispatched %d inference requests, want exactly one", after-before)
			}
			captured, path := lastUpstreamRequest(t, state)
			tools, ok := captured["tools"].([]any)
			if path != "/v1/messages" || !ok || len(tools) != 1 || nativeMap(t, tools[0])["name"] != "lookup" {
				t.Fatalf("nested supported function was not preserved: path=%q request=%+v", path, captured)
			}
			preview := map[string]any{"model": "claude-sonnet-5.5", "stream": stream, "input": "Search for a circle.", "tools": []any{map[string]any{"type": "web_search_preview"}}}
			status, response = postProxyWithSession(t, base+"/v1/responses", preview, fmt.Sprintf("native-search-preview-%t", stream))
			if status != http.StatusOK {
				t.Fatalf("legacy search translation returned %d: %s", status, response)
			}
			captured, path = lastUpstreamRequest(t, state)
			tools, ok = captured["tools"].([]any)
			if path != "/v1/messages" || !ok || len(tools) != 1 || nativeMap(t, tools[0])["type"] != "web_search_20250305" {
				t.Fatalf("legacy search tool did not retain native server-tool semantics: path=%q request=%+v", path, captured)
			}
			native := map[string]any{"model": "gpt-6-luna", "stream": stream, "input": "Draw a circle.", "tools": []any{map[string]any{"type": "image_generation", "quality": "low"}}}
			status, response = postProxyWithSession(t, base+"/v1/responses", native, fmt.Sprintf("native-image-pass-through-%t", stream))
			if status != http.StatusOK {
				t.Fatalf("native Responses pass-through returned %d: %s", status, response)
			}
			captured, path = lastUpstreamRequest(t, state)
			if path != "/responses" {
				t.Fatalf("native image tool reached %q, want Responses", path)
			}
			assertEqualJSON(t, captured["tools"], native["tools"])
			request := map[string]any{"model": "gpt-6-luna", "stream": stream, "max_tokens": 64, "messages": []any{map[string]any{"role": "system", "content": "Keep this system instruction.", "output_config": map[string]any{"effort": "high"}}, map[string]any{"role": "user", "content": "Say hello."}}}
			status, response = postProxyWithSession(t, base+"/v1/messages", request, fmt.Sprintf("native-system-effort-%t", stream))
			if status != http.StatusOK {
				t.Fatalf("system content with effort returned %d: %s", status, response)
			}
			captured, path = lastUpstreamRequest(t, state)
			if path != "/responses" || nativeMap(t, captured["reasoning"])["effort"] != "high" {
				t.Fatalf("system effort did not reach Responses: path=%q request=%+v", path, captured)
			}
			input, ok := captured["input"].([]any)
			if !ok || len(input) < 1 || nativeMap(t, input[0])["role"] != "system" || !containsJSONScalar(input[0], "Keep this system instruction.") {
				t.Fatalf("system authority or text was lost: %+v", captured)
			}
		})
	}
	t.Run("ConfiguredAbsentOverrideRoute", func(t *testing.T) {
		model := "fixture-configured-route"
		state := newNativeFixture(t)
		if bytes.Contains(state.modelCatalog, []byte(model)) {
			t.Fatalf("synthetic Copilot catalog unexpectedly contains configured model %q", model)
		}
		upstream := httptest.NewServer(state)
		defer upstream.Close()
		base := startProxyWithConfiguredEndpointOverride(t, binary, upstream.URL, model, "/responses")
		before := upstreamRequestCount(state)
		callProxyWithSession(t, base+"/v1/responses", map[string]any{"model": model, "input": "Use the configured route."}, "configured-absent-override")
		if after := upstreamRequestCount(state); after != before+1 {
			t.Fatalf("configured absent model dispatched %d inference requests, want one", after-before)
		}
		captured, path := lastUpstreamRequest(t, state)
		if path != "/responses" || captured["model"] != model {
			t.Fatalf("configured exact model routed to path=%q model=%v, want /responses and %q", path, captured["model"], model)
		}
	})
}

func startProxyWithConfiguredEndpointOverride(t *testing.T, binary, upstream, model, endpoint string) string {
	t.Helper()
	root := t.TempDir()
	base, stop := startProxyInRoot(t, binary, upstream, root, nil, "")
	stop()
	configPath := filepath.Join(root, "config.yaml")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read native test config: %v", err)
	}
	config := make(map[string]any)
	if err := yaml.Unmarshal(configBytes, &config); err != nil {
		t.Fatalf("decode native test config: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve native host port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release native host port: %v", err)
	}
	server := nativeMap(t, config["server"])
	server["port"] = port
	base = fmt.Sprintf("http://127.0.0.1:%d", port)
	plugins := nativeMap(t, config["plugins"])
	pluginConfigs := nativeMap(t, plugins["configs"])
	plugin := nativeMap(t, pluginConfigs["cliproxyapi-copilot"])
	plugin["model_endpoint_overrides"] = map[string]any{model: endpoint}
	configBytes, err = yaml.Marshal(config)
	if err != nil {
		t.Fatalf("encode native test config: %v", err)
	}
	if err := os.WriteFile(configPath, configBytes, 0600); err != nil {
		t.Fatalf("write native test config: %v", err)
	}
	logPath := filepath.Join(root, "host.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatalf("open native host log: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, binary, "--config", configPath, "--local-model")
	command.Env = filteredChildEnvironment(os.Environ())
	command.Dir = root
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		t.Fatalf("start configured native host: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = command.Wait()
		_ = logFile.Close()
	})
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			log, _ := os.ReadFile(logPath)
			t.Fatalf("configured native host did not register %q: %s", model, log)
		case <-ticker.C:
			request, _ := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
			request.Header.Set("Authorization", "Bearer fixture-client-key")
			response, err := client.Do(request)
			if err != nil {
				continue
			}
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				continue
			}
			var catalog struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if json.Unmarshal(body, &catalog) != nil {
				continue
			}
			for _, entry := range catalog.Data {
				if entry.ID == model {
					return base
				}
			}
		}
	}
}
