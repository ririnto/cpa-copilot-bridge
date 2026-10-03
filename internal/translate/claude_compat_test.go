package translate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestResponsesClaudeToolIDReplayPreservesItemAndCallIDs(t *testing.T) {
	const itemID = "fc_opaque+value/with/slash"
	const callID = "call_opaque+value/with/slash"
	responseBody := []byte(`{"id":"resp_1","status":"completed","model":"gpt-test","output":[{"id":"` + itemID + `","type":"function_call","call_id":"` + callID + `","name":"lookup","arguments":"{\"q\":\"x\"}"}],"usage":{"input_tokens":2,"output_tokens":1}}`)
	claudeResponse, err := ResponseFromEndpoint(context.Background(), EndpointResponses, "claude", "gpt-test", nil, nil, responseBody)
	if err != nil {
		t.Fatalf("translate Responses response: %v", err)
	}
	toolID := gjson.GetBytes(claudeResponse, "content.0.id").String()
	if !strings.HasPrefix(toolID, claudeToolIDPrefix) || !safeClaudeToolID(toolID) {
		t.Fatalf("Claude tool ID is not a safe reversible carrier: %q", toolID)
	}
	if gotItemID, gotCallID, ok := DecodeClaudeToolIDs(toolID); !ok || gotItemID != itemID || gotCallID != callID {
		t.Fatalf("decoded tool IDs = (%q, %q, %t), want (%q, %q, true)", gotItemID, gotCallID, ok, itemID, callID)
	}
	if gotItemID, gotCallID, ok := DecodeClaudeToolIDs("toolu_plain"); ok || gotItemID != "" || gotCallID != "toolu_plain" {
		t.Fatalf("plain Claude ID decode = (%q, %q, %t)", gotItemID, gotCallID, ok)
	}
	requestBody, err := json.Marshal(map[string]any{
		"messages": []any{
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": toolID, "name": "lookup", "input": map[string]any{"q": "x"}}}},
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": toolID, "content": "found"}}},
		},
	})
	if err != nil {
		t.Fatalf("encode Claude replay request: %v", err)
	}
	replayed, err := RequestForEndpointFrom("claude", "gpt-test", requestBody, false, EndpointResponses)
	if err != nil {
		t.Fatalf("translate Claude replay request: %v", err)
	}
	if got := gjson.GetBytes(replayed, "input.0.id").String(); got != itemID {
		t.Fatalf("function_call.id = %q, want %q; request=%s", got, itemID, replayed)
	}
	if got := gjson.GetBytes(replayed, "input.0.call_id").String(); got != callID {
		t.Fatalf("function_call.call_id = %q, want %q; request=%s", got, callID, replayed)
	}
	if got := gjson.GetBytes(replayed, "input.1.call_id").String(); got != callID {
		t.Fatalf("function_call_output.call_id = %q, want %q; request=%s", got, callID, replayed)
	}
}

func TestClaudeReasoningPayloadsPreserveOpaqueWhitespace(t *testing.T) {
	const thinking = " inspect carefully \n"
	const signature = " sig "
	const redacted = "\nopaque redacted\t"
	requestBody, err := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "thinking", "thinking": thinking, "signature": signature},
		map[string]any{"type": "redacted_thinking", "data": redacted},
	}}}})
	if err != nil {
		t.Fatalf("encode Claude reasoning request: %v", err)
	}
	responsesRequest, err := RequestForEndpointFrom("claude", "gpt-test", requestBody, false, EndpointResponses)
	if err != nil {
		t.Fatalf("translate Claude reasoning request: %v", err)
	}
	if gjson.GetBytes(responsesRequest, "input.0.summary.0.text").String() != thinking || gjson.GetBytes(responsesRequest, "input.0.encrypted_content").String() != signature {
		t.Fatalf("Claude thinking payload changed: %s", responsesRequest)
	}
	if got := gjson.GetBytes(responsesRequest, "input.1.encrypted_content").String(); got != redactedThinkingPrefix+redacted {
		t.Fatalf("Claude redacted payload changed: %q", got)
	}
	responseBody, err := json.Marshal(map[string]any{
		"id": "resp_reasoning", "status": "completed", "model": "gpt-test",
		"output": []any{
			map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": thinking}}, "encrypted_content": signature},
			map[string]any{"type": "reasoning", "summary": []any{}, "encrypted_content": redactedThinkingPrefix + redacted},
		},
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
	})
	if err != nil {
		t.Fatalf("encode Responses reasoning response: %v", err)
	}
	claudeResponse, err := ResponseFromEndpoint(context.Background(), EndpointResponses, "claude", "gpt-test", nil, nil, responseBody)
	if err != nil {
		t.Fatalf("translate Responses reasoning response: %v", err)
	}
	if got := gjson.GetBytes(claudeResponse, "content.0.thinking").String(); got != thinking {
		t.Fatalf("Claude thinking text = %q, want %q; response=%s", got, thinking, claudeResponse)
	}
	if got := gjson.GetBytes(claudeResponse, "content.0.signature").String(); got != signature {
		t.Fatalf("Claude thinking signature = %q, want %q; response=%s", got, signature, claudeResponse)
	}
	if got := gjson.GetBytes(claudeResponse, "content.1.data").String(); got != redacted {
		t.Fatalf("Claude redacted data = %q, want %q; response=%s", got, redacted, claudeResponse)
	}
}

func TestNativeProtocolPassthroughPreservesToolIDs(t *testing.T) {
	responsesRequest := []byte(`{"model":"old","stream":false,"input":[{"type":"function_call","id":"fc+opaque/1","call_id":"call+opaque/1","name":"lookup","arguments":"{}"}]}`)
	translatedRequest, err := RequestForEndpointFrom("openai-response", "new", responsesRequest, true, EndpointResponses)
	if err != nil {
		t.Fatalf("translate native Responses request: %v", err)
	}
	if got := gjson.GetBytes(translatedRequest, "input.0.id").String(); got != "fc+opaque/1" {
		t.Fatalf("native request item id = %q; request=%s", got, translatedRequest)
	}
	if got := gjson.GetBytes(translatedRequest, "input.0.call_id").String(); got != "call+opaque/1" {
		t.Fatalf("native request call_id = %q; request=%s", got, translatedRequest)
	}
	if got := gjson.GetBytes(translatedRequest, "model").String(); got != "new" {
		t.Fatalf("native request model = %q", got)
	}
	if !gjson.GetBytes(translatedRequest, "stream").Bool() {
		t.Fatal("native request stream flag was not updated")
	}
	responsesBody := []byte(`{"id":"resp_1","status":"completed","output":[{"id":"fc+opaque/1","type":"function_call","call_id":"call+opaque/1","name":"lookup","arguments":"{}"}]}`)
	responsesBodyOut, err := ResponseFromEndpoint(context.Background(), EndpointResponses, "openai-response", "new", nil, nil, responsesBody)
	if err != nil {
		t.Fatalf("pass through native Responses response: %v", err)
	}
	if string(responsesBodyOut) != string(responsesBody) {
		t.Fatalf("native Responses response changed: %s", responsesBodyOut)
	}
	claudeRequest := []byte(`{"model":"old","stream":false,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_a/b+","name":"lookup","input":{}}]}]}`)
	claudeRequestOut, err := RequestForEndpointFrom("claude", "new", claudeRequest, true, EndpointMessages)
	if err != nil {
		t.Fatalf("pass through native Claude request: %v", err)
	}
	if got := gjson.GetBytes(claudeRequestOut, "messages.0.content.0.id").String(); got != "toolu_a/b+" {
		t.Fatalf("native Claude tool ID = %q; request=%s", got, claudeRequestOut)
	}
	claudeResponse := []byte(`{"type":"message","content":[{"type":"tool_use","id":"toolu_a/b+"}]}`)
	claudeResponseOut, err := ResponseFromEndpoint(context.Background(), EndpointMessages, "claude", "new", nil, nil, claudeResponse)
	if err != nil {
		t.Fatalf("pass through native Claude response: %v", err)
	}
	if string(claudeResponseOut) != string(claudeResponse) {
		t.Fatalf("native Claude response changed: %s", claudeResponseOut)
	}
	frame := []byte("event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"fc+opaque/1\",\"type\":\"function_call\",\"call_id\":\"call+opaque/1\"}}\n\n")
	var state any
	frames, err := StreamFromEndpoint(context.Background(), EndpointResponses, "openai-response", "new", nil, nil, frame, &state)
	if err != nil {
		t.Fatalf("pass through native Responses stream: %v", err)
	}
	if len(frames) != 1 || string(frames[0]) != string(frame) {
		t.Fatalf("native Responses stream changed: %q", frames)
	}
}

func TestResponsesStreamPreservesTerminalReasoningAndLateUsage(t *testing.T) {
	chunks := [][]byte{
		[]byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"status\":\"in_progress\",\"model\":\"gpt-test\",\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n"),
		[]byte("event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"rs_1\",\"type\":\"reasoning\",\"summary\":[]}}\n\n"),
		[]byte("event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"output_index\":0,\"item_id\":\"rs_1\",\"delta\":\"think\"}\n\n"),
		[]byte("event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"rs_1\",\"type\":\"reasoning\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"think\"}]}}\n\n"),
		[]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"model\":\"gpt-test\",\"output\":[{\"id\":\"rs_1\",\"type\":\"reasoning\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"think\"}],\"encrypted_content\":\"terminal-signature\"}],\"usage\":{\"input_tokens\":11,\"output_tokens\":7,\"input_tokens_details\":{\"cached_tokens\":4}}}}\n\n"),
	}
	var state any
	var payloads []gjson.Result
	for _, chunk := range chunks {
		frames, err := ResponsesSSEToClaude(context.Background(), "gpt-test", nil, nil, chunk, &state)
		if err != nil {
			t.Fatalf("translate Responses event: %v", err)
		}
		payloads = append(payloads, claudePayloads(t, frames)...)
	}
	signatureCount := 0
	var finalUsage gjson.Result
	for _, payload := range payloads {
		if payload.Get("delta.type").String() == "signature_delta" && payload.Get("delta.signature").String() == "terminal-signature" {
			signatureCount++
		}
		if payload.Get("type").String() == "message_delta" {
			finalUsage = payload.Get("usage")
		}
	}
	if signatureCount != 1 {
		t.Fatalf("terminal signature count = %d; payloads=%v", signatureCount, payloads)
	}
	if finalUsage.Get("input_tokens").Int() != 11 || finalUsage.Get("output_tokens").Int() != 7 || finalUsage.Get("cache_read_input_tokens").Int() != 4 {
		t.Fatalf("late final usage was not preserved: %s", finalUsage.Raw)
	}
}

func TestResponsesTerminalSignatureOnlyReasoning(t *testing.T) {
	chunk := []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_2\",\"status\":\"completed\",\"model\":\"gpt-test\",\"output\":[{\"id\":\"rs_2\",\"type\":\"reasoning\",\"summary\":[],\"encrypted_content\":\"signature-only\"}],\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n")
	var state any
	frames, err := ResponsesSSEToClaude(context.Background(), "gpt-test", nil, nil, chunk, &state)
	if err != nil {
		t.Fatalf("translate terminal reasoning response: %v", err)
	}
	payloads := claudePayloads(t, frames)
	var signatureCount int
	for _, payload := range payloads {
		if payload.Get("delta.type").String() == "signature_delta" && payload.Get("delta.signature").String() == "signature-only" {
			signatureCount++
		}
	}
	if signatureCount != 1 {
		t.Fatalf("signature-only reasoning emitted %d signatures; frames=%q", signatureCount, frames)
	}
}

func TestResponsesStreamTerminalItemsReplayOnNextClaudeRequest(t *testing.T) {
	const itemID = "fc+terminal/item"
	const callID = "call+terminal/item"
	chunk := []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"model\":\"gpt-test\",\"output\":[{\"id\":\"" + itemID + "\",\"type\":\"function_call\",\"call_id\":\"" + callID + "\",\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":\\\"x\\\"}\"},{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}],\"usage\":{\"input_tokens\":5,\"output_tokens\":3}}}\n\n")
	var state any
	frames, err := ResponsesSSEToClaude(context.Background(), "gpt-test", nil, nil, chunk, &state)
	if err != nil {
		t.Fatalf("translate terminal response: %v", err)
	}
	payloads := claudePayloads(t, frames)
	var toolID string
	var sawTerminalText bool
	for _, payload := range payloads {
		if payload.Get("content_block.type").String() == "tool_use" {
			toolID = payload.Get("content_block.id").String()
		}
		if payload.Get("delta.text").String() == "done" {
			sawTerminalText = true
		}
	}
	if toolID == "" || !safeClaudeToolID(toolID) {
		t.Fatalf("terminal tool ID missing or unsafe: %q; frames=%q", toolID, frames)
	}
	if !sawTerminalText {
		t.Fatalf("response.completed text was not emitted: %q", frames)
	}
	requestBody, err := json.Marshal(map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": toolID, "name": "lookup", "input": map[string]any{"q": "x"}}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": toolID, "content": "found"}}},
	}})
	if err != nil {
		t.Fatalf("encode follow-up Claude request: %v", err)
	}
	replayed, err := RequestForEndpointFrom("claude", "gpt-test", requestBody, false, EndpointResponses)
	if err != nil {
		t.Fatalf("translate follow-up Claude request: %v", err)
	}
	if gjson.GetBytes(replayed, "input.0.id").String() != itemID || gjson.GetBytes(replayed, "input.0.call_id").String() != callID || gjson.GetBytes(replayed, "input.1.call_id").String() != callID {
		t.Fatalf("terminal tool identity did not replay: %s", replayed)
	}
}

func claudePayloads(t *testing.T, frames [][]byte) []gjson.Result {
	t.Helper()
	var payloads []gjson.Result
	for _, frame := range frames {
		_, data, _, err := parseSSEFrame(frame)
		if err != nil {
			t.Fatalf("parse Claude SSE event: %v", err)
		}
		if len(data) > 0 {
			payloads = append(payloads, gjson.ParseBytes(data))
		}
	}
	return payloads
}
