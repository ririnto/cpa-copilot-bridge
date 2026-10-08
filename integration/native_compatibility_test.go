package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
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
}
