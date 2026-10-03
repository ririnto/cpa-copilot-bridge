package provider

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ririnto/cpa-copilot-bridge/internal/translate"
)

func TestStreamTerminalRequiresSourceSuccessEvent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		endpoint  string
		frame     string
		wantDone  bool
		wantError bool
	}{
		{name: "Responses completed", endpoint: translate.EndpointResponses, frame: "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[]}}\n\n", wantDone: true},
		{name: "Responses incomplete", endpoint: translate.EndpointResponses, frame: "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\"}}\n\n", wantError: true},
		{name: "Responses failed", endpoint: translate.EndpointResponses, frame: "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n", wantError: true},
		{name: "Responses error", endpoint: translate.EndpointResponses, frame: "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"failed\"}}\n\n", wantError: true},
		{name: "Responses DONE is not terminal", endpoint: translate.EndpointResponses, frame: "data: [DONE]\n\n", wantError: true},
		{name: "Chat completion sentinel", endpoint: translate.EndpointChatCompletions, frame: "data: [DONE]\n\n", wantDone: true},
		{name: "Chat error", endpoint: translate.EndpointChatCompletions, frame: "data: {\"error\":{\"message\":\"failed\"}}\n\n", wantError: true},
		{name: "Messages stop", endpoint: translate.EndpointMessages, frame: "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", wantDone: true},
		{name: "Messages error", endpoint: translate.EndpointMessages, frame: "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"failed\"}}\n\n", wantError: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var terminal streamTerminal
			done, err := terminal.observe(test.endpoint, []byte(test.frame), "secret-copilot", "ghp_123456789012345678901234")
			if (err != nil) != test.wantError {
				t.Fatalf("observe error = %v, wantError=%v", err, test.wantError)
			}
			if done != test.wantDone || terminal.completed != test.wantDone {
				t.Fatalf("done/completed = %v/%v, want %v", done, terminal.completed, test.wantDone)
			}
			if test.endpoint == translate.EndpointResponses && done && !strings.Contains(string(terminal.response), `"id":"resp_1"`) {
				t.Fatalf("terminal response = %s", terminal.response)
			}
		})
	}
}

func TestProtocolSessionIdentityAndPromptCacheScope(t *testing.T) {
	t.Parallel()
	headers := http.Header{"session_id": []string{"codex-session-7"}}
	session, agent := protocolSessionIdentity(nil, headers, nil)
	if session != "codex-session-7" || agent != "main" {
		t.Fatalf("session/agent = %q/%q", session, agent)
	}
	scope := protocolScopeKey("auth-a", "github-token-a", "model-a", "https://api.githubcopilot.com", translate.EndpointResponses, session, agent, 0)
	if got := derivedPromptCacheKey(scope); got == "" || got != derivedPromptCacheKey(protocolScopeKey("auth-a", "github-token-a", "model-a", "https://api.githubcopilot.com", translate.EndpointResponses, session, agent, 0)) {
		t.Fatalf("derived cache key is not deterministic: %q", got)
	}
	for _, changed := range []string{
		protocolScopeKey("auth-b", "github-token-a", "model-a", "https://api.githubcopilot.com", translate.EndpointResponses, session, agent, 0),
		protocolScopeKey("auth-a", "github-token-b", "model-a", "https://api.githubcopilot.com", translate.EndpointResponses, session, agent, 0),
		protocolScopeKey("auth-a", "github-token-a", "model-b", "https://api.githubcopilot.com", translate.EndpointResponses, session, agent, 0),
		protocolScopeKey("auth-a", "github-token-a", "model-a", "https://api.githubcopilot.com", translate.EndpointChatCompletions, session, agent, 0),
	} {
		if derivedPromptCacheKey(changed) == derivedPromptCacheKey(scope) {
			t.Fatal("cache key crossed credential, model, or endpoint scope")
		}
	}
	if got := derivedPromptCacheKey(protocolScopeKey("auth-a", "github-token-a", "model-a", "https://api.githubcopilot.com", translate.EndpointResponses, "", agent, 0)); got != "" {
		t.Fatalf("cache key without session identity = %q", got)
	}
}

func TestExplicitPromptCacheKeyWinsAndCompactionTriggerHelpers(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"prompt_cache_key":"caller-key","input":[{"type":"message","role":"user","content":"hi"}]}`)
	if got := explicitPromptCacheKey(payload, map[string]any{"prompt_cache_key": "metadata-key"}); got != "caller-key" {
		t.Fatalf("explicit caller key = %q", got)
	}
	if hasCompactionTrigger(payload) {
		t.Fatal("ordinary Responses input contained a compaction trigger")
	}
	compactBody, err := addCompactionTrigger(payload)
	if err != nil {
		t.Fatalf("add trigger: %v", err)
	}
	if !hasCompactionTrigger(compactBody) {
		t.Fatalf("compaction trigger missing from %s", compactBody)
	}
	stringInput, err := addCompactionTrigger([]byte(`{"input":"earlier messages"}`))
	if err != nil || !hasCompactionTrigger(stringInput) {
		t.Fatalf("add trigger to string input: body=%s error=%v", stringInput, err)
	}
}
