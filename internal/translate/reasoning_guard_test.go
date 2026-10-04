package translate

import (
	"context"
	"strings"
	"testing"
)

func TestOpaqueReasoningRejectsLossyProtocolRoutes(t *testing.T) {
	signedClaude := []byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"reason","signature":"opaque-signature"}]}]}`)
	if _, err := RequestForEndpointFrom("claude", "gpt-test", signedClaude, false, EndpointChatCompletions); err == nil || !strings.Contains(err.Error(), "foreign encrypted Responses reasoning") {
		t.Fatalf("signed Claude request error = %v", err)
	}
	if _, err := RequestForEndpointFrom("claude", "gpt-test", signedClaude, false, EndpointResponses); err != nil {
		t.Fatalf("custom Claude-to-Responses request rejected signed reasoning: %v", err)
	}
	redactedClaude := []byte(`{"messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"opaque-data"}]}]}`)
	if _, err := RequestForEndpointFrom("claude", "gpt-test", redactedClaude, false, EndpointChatCompletions); err == nil {
		t.Fatal("redacted Claude reasoning was silently converted to Chat Completions")
	}
	responsesHistory := []byte(`{"input":[{"type":"reasoning","summary":[],"encrypted_content":"opaque-signature"}]}`)
	if _, err := RequestForEndpointFrom("openai-response", "gpt-test", responsesHistory, false, EndpointChatCompletions); err == nil {
		t.Fatal("encrypted Responses history was silently converted to Chat Completions")
	}
	plainClaude := []byte(`{"messages":[{"role":"assistant","content":[{"type":"text","text":"plain answer"}]}]}`)
	if _, err := RequestForEndpointFrom("claude", "gpt-test", plainClaude, false, EndpointChatCompletions); err != nil {
		t.Fatalf("plain Claude content was rejected: %v", err)
	}
	responsesOutput := []byte(`{"id":"resp_1","status":"completed","output":[{"type":"reasoning","summary":[],"encrypted_content":"opaque-signature"}]}`)
	if _, err := ResponseFromEndpoint(context.Background(), EndpointResponses, "openai", "gpt-test", nil, nil, responsesOutput); err == nil {
		t.Fatal("encrypted Responses output was silently converted to Chat Completions")
	}
	if _, err := ResponseFromEndpoint(context.Background(), EndpointResponses, "claude", "gpt-test", nil, nil, responsesOutput); err != nil {
		t.Fatalf("custom Responses-to-Claude response rejected opaque reasoning: %v", err)
	}
	claudeOutput := []byte(`{"type":"message","content":[{"type":"thinking","thinking":"reason","signature":"opaque-signature"}]}`)
	if _, err := ResponseFromEndpoint(context.Background(), EndpointMessages, "openai-response", "gpt-test", nil, nil, claudeOutput); err == nil {
		t.Fatal("signed Claude output was silently converted to Responses")
	}
}

func TestOpaqueReasoningRejectsLossyStreamingRoutes(t *testing.T) {
	claudeFrame := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"opaque-signature\"}}\n\n")
	var claudeState any
	if _, err := StreamFromEndpoint(context.Background(), EndpointMessages, "openai-response", "gpt-test", nil, nil, claudeFrame, &claudeState); err == nil {
		t.Fatal("Claude signature stream was silently converted")
	}
	responsesFrame := []byte("event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"opaque-signature\"}}\n\n")
	var responsesState any
	if _, err := StreamFromEndpoint(context.Background(), EndpointResponses, "openai", "gpt-test", nil, nil, responsesFrame, &responsesState); err == nil {
		t.Fatal("Responses signature stream was silently converted")
	}
}
