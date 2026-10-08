package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/arthur-sommer-etc/cliproxyapi-copilot-plugin/internal/translate"
	"github.com/arthur-sommer-etc/cliproxyapi-copilot-plugin/internal/transport"
	"github.com/tidwall/gjson"
)

type nativeToolHost struct {
	*compactionStreamHost
	errors        []string
	bodies        [][]byte
	closedStreams []string
	messages      bool
}

func (h *nativeToolHost) Do(ctx context.Context, callback string, request transport.Request) (transport.Response, error) {
	if request.Method == http.MethodGet {
		return h.compactionStreamHost.Do(ctx, callback, request)
	}
	h.bodies = append(h.bodies, append([]byte(nil), request.Body...))
	if len(h.bodies) <= len(h.errors) {
		return transport.Response{StatusCode: http.StatusBadRequest, Body: []byte(h.errors[len(h.bodies)-1])}, nil
	}
	return transport.Response{StatusCode: http.StatusOK, Body: h.responseBody}, nil
}

func (h *nativeToolHost) OpenStream(_ context.Context, _ string, request transport.Request) (transport.Stream, error) {
	h.bodies = append(h.bodies, append([]byte(nil), request.Body...))
	status := http.StatusOK
	if len(h.bodies) <= len(h.errors) {
		status = http.StatusBadRequest
	}
	return transport.Stream{ID: fmt.Sprintf("native-%d", len(h.bodies)), StatusCode: status}, nil
}

func (h *nativeToolHost) ReadStream(_ context.Context, id string) (transport.StreamChunk, error) {
	var index int
	if _, err := fmt.Sscanf(id, "native-%d", &index); err != nil {
		return transport.StreamChunk{}, err
	}
	if index <= len(h.errors) {
		return transport.StreamChunk{Payload: []byte(h.errors[index-1]), Done: true}, nil
	}
	if h.messages {
		return transport.StreamChunk{Payload: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{" + strings.TrimPrefix(string(h.responseBody), "{") + "}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"), Done: true}, nil
	}
	return transport.StreamChunk{Payload: []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{" + strings.TrimPrefix(string(h.responseBody), "{") + "}\n\n"), Done: true}, nil
}

func (h *nativeToolHost) CloseStream(_ context.Context, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closedStreams = append(h.closedStreams, id)
	return nil
}

func unsupportedNativeTool(index int) string {
	return fmt.Sprintf(`{"error":{"code":"unsupported_value","type":"invalid_request_error","param":"tools[%d].type","message":"Unsupported tool type"}}`, index)
}

func TestNativeToolUpstreamDoesNotRedispatch(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, test := range []struct {
			name         string
			tools        string
			choice       string
			errors       []string
			wantCalls    int
			wantError    bool
			wantExcluded bool
		}{
			{name: "required without tools fails before inference", tools: `[]`, choice: `,"tool_choice":"required"`, wantCalls: 0, wantError: true},
			{name: "any without tools fails before inference", tools: `[]`, choice: `,"tool_choice":{"type":"any"}`, wantCalls: 0, wantError: true},
			{name: "supported pass through", tools: `[{"type":"web_search","max_uses":2},{"type":"image_generation","size":"1024x1024"}]`, wantCalls: 1},
			{name: "attested unsupported tool does not retry", tools: `[{"type":"web_search"},{"type":"image_generation"},{"type":"function","name":"web_search","parameters":{"type":"object"}}]`, errors: []string{unsupportedNativeTool(0), unsupportedNativeTool(0)}, wantCalls: 1, wantError: true},
			{name: "unknown error unchanged", tools: `[{"type":"web_search"}]`, errors: []string{`{"error":{"code":"invalid_value","param":"tools[0].type"}}`}, wantCalls: 1, wantError: true},
			{name: "unrelated option unchanged", tools: `[{"type":"web_search"}]`, errors: []string{`{"error":{"code":"unsupported_value","param":"tools[0].max_uses"}}`}, wantCalls: 1, wantError: true},
			{name: "forced native fails", tools: `[{"type":"web_search"}]`, choice: `,"tool_choice":{"type":"web_search"}`, errors: []string{unsupportedNativeTool(0)}, wantCalls: 1, wantError: true},
			{name: "required empty fails", tools: `[{"type":"web_search"}]`, choice: `,"tool_choice":"required"`, errors: []string{unsupportedNativeTool(0)}, wantCalls: 1, wantError: true},
			{name: "automatic unsupported does not retry", tools: `[{"type":"web_search"}]`, choice: `,"tool_choice":"auto"`, errors: []string{unsupportedNativeTool(0)}, wantCalls: 1, wantError: true},
			{name: "multiple unsupported tools do not retry", tools: `[{"type":"web_search"},{"type":"web_search_preview"},{"type":"image_generation"}]`, errors: []string{unsupportedNativeTool(0), unsupportedNativeTool(0), unsupportedNativeTool(0)}, wantCalls: 1, wantError: true},
			{name: "function attestation never excludes", tools: `[{"type":"function","name":"web_search","parameters":{"type":"object"}}]`, errors: []string{unsupportedNativeTool(0)}, wantCalls: 1, wantError: true},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", test.name, streaming), func(t *testing.T) {
				host := &nativeToolHost{compactionStreamHost: newCompactionStreamHost(compactionStreamResponse("completed")), errors: test.errors}
				base := newCompactionStreamService(t, host.compactionStreamHost)
				base.host = host
				request := compactionStreamRequest([]byte(`{"input":"Hello","tools":`+test.tools+test.choice+`}`), "")
				var headers http.Header
				var err error
				if streaming {
					headers, err = base.ExecuteStream(context.Background(), request)
				} else {
					response, executeErr := base.Execute(context.Background(), request)
					headers, err = response.Headers, executeErr
					if err == nil && test.wantExcluded && response.Metadata["copilot_excluded_native_tools"] == nil {
						t.Fatal("missing exclusion metadata")
					}
					if err == nil && string(response.Payload) != string(host.responseBody) {
						t.Fatalf("fabricated response: %s", response.Payload)
					}
				}
				if (err != nil) != test.wantError {
					t.Fatalf("error=%v, wantError=%t", err, test.wantError)
				}
				if len(host.bodies) != test.wantCalls {
					t.Fatalf("calls=%d want %d", len(host.bodies), test.wantCalls)
				}
				if err == nil && test.wantExcluded && headers.Get("X-Copilot-Excluded-Native-Tools") == "" {
					t.Fatal("missing exclusion header")
				}
				if streaming && err == nil {
					_, message := collectCompactionStreamFrames(t, host.compactionStreamHost)
					if message != "" {
						t.Fatalf("stream failure: %s", message)
					}
				}
				if streaming {
					host.mu.Lock()
					closed := len(host.closedStreams)
					host.mu.Unlock()
					if closed != test.wantCalls {
						t.Fatalf("closed %d streams for %d calls", closed, test.wantCalls)
					}
				}
				if test.name == "supported pass through" && gjson.GetBytes(host.bodies[0], "tools").Raw != test.tools {
					t.Fatalf("supported tools changed: %s", host.bodies[0])
				}
			})
		}
	}
}

func TestNativeToolRepresentabilityFilter(t *testing.T) {
	for _, test := range []struct {
		source, endpoint, body string
		want                   int
		wantError              bool
	}{
		{"openai-response", translate.EndpointChatCompletions, `{"tools":[{"type":"web_search"},{"type":"image_generation"},{"type":"function","name":"image_generation"}]}`, 1, false},
		{"openai-response", translate.EndpointMessages, `{"tools":[{"type":"web_search","max_uses":2},{"type":"image_generation"}]}`, 1, false},
		{"openai-response", translate.EndpointResponses, `{"tools":[{"type":"web_search"},{"type":"image_generation"}]}`, 2, false},
		{"claude", translate.EndpointResponses, `{"tools":[{"type":"web_search_20250305","name":"web_search"},{"name":"web_search","input_schema":{"type":"object"}}],"tool_choice":{"type":"tool","name":"web_search"}}`, 1, false},
		{"openai-response", translate.EndpointChatCompletions, `{"tools":[{"type":"web_search"}],"tool_choice":"auto"}`, 0, false},
		{"openai-response", translate.EndpointChatCompletions, `{"tools":[{"type":"web_search"}],"tool_choice":"required"}`, 0, true},
		{"openai-response", translate.EndpointChatCompletions, `{"tools":[{"type":"web_search"}],"tool_choice":{"type":"web_search"}}`, 0, true},
		{"openai-response", translate.EndpointChatCompletions, `{"tools":[{"type":"image_generation"},{"type":"function","name":"image_generation","parameters":{"type":"object"}}],"tool_choice":{"type":"function","name":"image_generation"}}`, 1, false},
		{"claude", translate.EndpointChatCompletions, `{"tools":[{"type":"web_search_20250305","name":"web_search"}],"tool_choice":{"type":"tool","name":"web_search"}}`, 0, true},
	} {
		t.Run(test.source+test.endpoint+test.body, func(t *testing.T) {
			body, exclusions, err := filterUnrepresentableNativeTools(test.source, test.endpoint, []byte(test.body))
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v", err)
			}
			if err == nil && len(gjson.GetBytes(body, "tools").Array()) != test.want {
				t.Fatalf("filtered request=%s", body)
			}
			if err == nil && test.want == 0 && gjson.GetBytes(body, "tool_choice").String() == "auto" {
				t.Fatalf("empty automatic choice: %s", body)
			}
			if err == nil && len(exclusions) > 0 {
				headers := nativeToolResponseHeaders(http.Header{}, exclusions)
				if !strings.Contains(headers.Get("X-Copilot-Excluded-Native-Tools"), "reason=unrepresentable_by_selected_endpoint") {
					t.Fatalf("missing explicit reason: %v", headers)
				}
			}
		})
	}
}

func TestNativeToolExclusionExecuteAndStream(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, choice := range []string{`{"type":"auto"}`, `{"type":"any"}`, `{"type":"tool","name":"web_search"}`} {
			t.Run(fmt.Sprintf("stream=%t/choice=%s", streaming, choice), func(t *testing.T) {
				host := &nativeToolHost{compactionStreamHost: newCompactionStreamHost(compactionStreamResponse("completed"))}
				service := newCompactionStreamService(t, host.compactionStreamHost)
				service.host = host
				payload := []byte(`{"messages":[{"role":"user","content":"Hello"}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":2},{"type":"image_generation","name":"image_generation"},{"name":"lookup","input_schema":{"type":"object"}}],"tool_choice":` + choice + `}`)
				request := compactionStreamRequest(payload, "")
				request.SourceFormat = "claude"
				var headers http.Header
				var err error
				if streaming {
					headers, err = service.ExecuteStream(context.Background(), request)
				} else {
					response, executeErr := service.Execute(context.Background(), request)
					headers, err = response.Headers, executeErr
					if err == nil && response.Metadata["copilot_excluded_native_tools"] == nil {
						t.Fatal("missing response exclusion metadata")
					}
					if strings.Contains(string(response.Payload), "web_search_result") || strings.Contains(string(response.Payload), "image_generation_call") {
						t.Fatalf("fabricated native result: %s", response.Payload)
					}
				}
				if choice == `{"type":"tool","name":"web_search"}` {
					if err == nil || !strings.Contains(err.Error(), "forces an unsupported native") || len(host.bodies) != 0 {
						t.Fatalf("forced choice error=%v calls=%d", err, len(host.bodies))
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(host.bodies) != 1 || gjson.GetBytes(host.bodies[0], "tools.0.name").String() != "lookup" || len(gjson.GetBytes(host.bodies[0], "tools").Array()) != 1 {
					t.Fatalf("ordinary tool preservation failed: %s", host.bodies)
				}
				if !strings.Contains(headers.Get("X-Copilot-Excluded-Native-Tools"), "web_search_20250305;reason=unrepresentable_by_selected_endpoint") || !strings.Contains(headers.Get("X-Copilot-Excluded-Native-Tools"), "image_generation;reason=unrepresentable_by_selected_endpoint") {
					t.Fatalf("missing explicit exclusions: %v", headers)
				}
				if streaming {
					frames, message := collectCompactionStreamFrames(t, host.compactionStreamHost)
					if message != "" || len(frames) == 0 {
						t.Fatalf("stream termination: message=%q frames=%d", message, len(frames))
					}
					host.mu.Lock()
					closed := len(host.closedStreams)
					host.mu.Unlock()
					if closed != 1 {
						t.Fatalf("closed %d upstream streams", closed)
					}
				}
			})
		}
	}
}

func TestAdditionalNativeToolsExecuteAndStream(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, choice := range []string{`"auto"`, `"required"`, `{"type":"image_generation"}`, `{"type":"function","name":"image_generation"}`} {
			t.Run(fmt.Sprintf("stream=%t/choice=%s", streaming, choice), func(t *testing.T) {
				host := &nativeToolHost{compactionStreamHost: newCompactionStreamHost([]byte(`{"id":"msg_test","type":"message","role":"assistant","model":"gpt-5.6-sol","content":[{"type":"text","text":"Hello"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)), messages: true}
				service := newCompactionStreamService(t, host.compactionStreamHost)
				service.host = host
				token := service.tokenEntries["auth-id"]
				if err := service.Configure([]byte("model_endpoint_overrides:\n  gpt-5.6-sol: /v1/messages\n")); err != nil {
					t.Fatal(err)
				}
				_, generation := service.configSnapshot()
				token.ConfigGeneration = generation
				service.tokenEntries["auth-id"] = token
				payload := []byte(`{"input":[{"type":"message","role":"user","content":"Hello"},{"type":"additional_tools","tools":[{"type":"image_generation","size":"1024x1024"},{"type":"function","name":"image_generation","description":"ordinary function","parameters":{"type":"object"}}]}],"tool_choice":` + choice + `}`)
				request := compactionStreamRequest(payload, "")
				var headers http.Header
				var err error
				if streaming {
					headers, err = service.ExecuteStream(context.Background(), request)
				} else {
					response, executeErr := service.Execute(context.Background(), request)
					headers, err = response.Headers, executeErr
					if err == nil && response.Metadata["copilot_excluded_native_tools"] == nil {
						t.Fatal("missing nested exclusion metadata")
					}
				}
				if choice == `{"type":"image_generation"}` {
					if err == nil || !strings.Contains(err.Error(), "forces an unsupported native") || len(host.bodies) != 0 {
						t.Fatalf("forced nested choice error=%v calls=%d", err, len(host.bodies))
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(host.bodies) != 1 || len(gjson.GetBytes(host.bodies[0], "tools").Array()) != 1 || gjson.GetBytes(host.bodies[0], "tools.0.name").String() != "image_generation" || gjson.GetBytes(host.bodies[0], "tools.0.description").String() != "ordinary function" {
					t.Fatalf("nested ordinary function was lost: %s", host.bodies)
				}
				if !strings.Contains(headers.Get("X-Copilot-Excluded-Native-Tools"), "image_generation;reason=unrepresentable_by_selected_endpoint") {
					t.Fatalf("nested exclusion not disclosed: %v", headers)
				}
				if streaming {
					_, message := collectCompactionStreamFrames(t, host.compactionStreamHost)
					if message != "" {
						t.Fatalf("nested stream failure: %s", message)
					}
					host.mu.Lock()
					closed := len(host.closedStreams)
					host.mu.Unlock()
					if closed != 1 {
						t.Fatalf("closed %d nested upstream streams", closed)
					}
				}
			})
		}
	}
}

func TestAdditionalNativeToolAggregateChoice(t *testing.T) {
	for _, test := range []struct {
		name, body string
		wantError  bool
	}{
		{"absent required", `{"tool_choice":"required"}`, true},
		{"empty any", `{"tools":[],"tool_choice":{"type":"any"}}`, true},
		{"nested required empties", `{"input":[{"type":"additional_tools","tools":[{"type":"image_generation"}]}],"tool_choice":"required"}`, true},
		{"nested auto empties", `{"input":[{"type":"additional_tools","tools":[{"type":"image_generation"}]}],"tool_choice":"auto"}`, false},
		{"top removed nested retained", `{"tools":[{"type":"image_generation"}],"input":[{"type":"additional_tools","tools":[{"type":"function","name":"image_generation","parameters":{"type":"object"}}]}],"tool_choice":{"type":"function","name":"image_generation"}}`, false},
		{"nested removed top retained", `{"tools":[{"type":"function","name":"image_generation","parameters":{"type":"object"}}],"input":[{"type":"additional_tools","tools":[{"type":"image_generation"}]}],"tool_choice":{"type":"function","name":"image_generation"}}`, false},
		{"mixed supported options and order", `{"tools":[{"type":"web_search","max_uses":2},{"type":"function","name":"top","parameters":{"type":"object"}}],"input":[{"type":"additional_tools","tools":[{"type":"function","name":"first","parameters":{"type":"object"}},{"type":"image_generation"},{"type":"function","name":"second","parameters":{"type":"object"}}]}],"tool_choice":"required"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, exclusions, err := filterUnrepresentableNativeTools("openai-response", translate.EndpointMessages, []byte(test.body))
			if (err != nil) != test.wantError {
				t.Fatalf("choice error=%v, wantError=%t", err, test.wantError)
			}
			if err != nil {
				return
			}
			if len(exclusions) != 1 || exclusions[0].Type != "image_generation" {
				t.Fatalf("exclusions=%v", exclusions)
			}
			if test.name == "nested auto empties" && gjson.GetBytes(body, "tool_choice").Exists() {
				t.Fatalf("empty nested auto choice remained: %s", body)
			}
			if test.name == "mixed supported options and order" && (gjson.GetBytes(body, "tools").Raw != gjson.Get(test.body, "tools").Raw || gjson.GetBytes(body, "input.0.tools.0.name").String() != "first" || gjson.GetBytes(body, "input.0.tools.1.name").String() != "second") {
				t.Fatalf("supported declarations/options/order changed: %s", body)
			}
		})
	}
	for _, body := range []string{`{"tool_choice":"required"}`, `{"tools":[],"tool_choice":"required"}`} {
		if _, _, err := filterUnrepresentableNativeTools("openai-response", translate.EndpointResponses, []byte(body)); err == nil {
			t.Fatalf("native Responses impossible choice accepted: %s", body)
		}
	}
	validNative := `{"tools":[{"type":"web_search","max_uses":2}],"input":[{"type":"additional_tools","tools":[{"type":"image_generation","size":"1024x1024"}]}],"tool_choice":"required"}`
	unchanged, exclusions, err := filterUnrepresentableNativeTools("openai-response", translate.EndpointResponses, []byte(validNative))
	if err != nil || len(exclusions) != 0 || string(unchanged) != validNative {
		t.Fatalf("valid native Responses declarations changed: body=%s exclusions=%v error=%v", unchanged, exclusions, err)
	}
}
