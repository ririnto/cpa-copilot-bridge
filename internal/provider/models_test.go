package provider

import (
	"testing"

	"github.com/ririnto/cpa-copilot-bridge/internal/translate"
)

func TestSelectEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		model        upstreamModel
		sourceFormat string
		want         string
		wantError    bool
	}{
		{
			name:         "responses source preserves native endpoint",
			model:        upstreamModel{ID: "model-a", SupportedEndpoints: []string{"/chat/completions", "/responses"}},
			sourceFormat: "openai-response",
			want:         translate.EndpointResponses,
		},
		{
			name:         "Claude prefers native Messages endpoint",
			model:        upstreamModel{ID: "model-b", SupportedEndpoints: []string{"/responses", "messages"}},
			sourceFormat: "claude",
			want:         translate.EndpointMessages,
		},
		{
			name:         "chat source prefers native Chat endpoint",
			model:        upstreamModel{ID: "model-c", SupportedEndpoints: []string{"/responses", "/chat/completions"}},
			sourceFormat: "openai",
			want:         translate.EndpointChatCompletions,
		},
		{
			name:         "Claude falls back to Responses",
			model:        upstreamModel{ID: "model-d", SupportedEndpoints: []string{"/chat/completions", "/responses"}},
			sourceFormat: "claude",
			want:         translate.EndpointResponses,
		},
		{
			name: "Claude client routes to Gemini Chat",
			model: upstreamModel{
				ID: "gemini-3.8-flash", Vendor: "Google",
				SupportedEndpoints: []string{"/chat/completions"},
			},
			sourceFormat: "claude",
			want:         translate.EndpointChatCompletions,
		},
		{
			name: "Claude client routes to native GPT Responses",
			model: upstreamModel{
				ID: "gpt-6-luna", Vendor: "OpenAI",
				SupportedEndpoints: []string{"/responses"},
			},
			sourceFormat: "claude",
			want:         translate.EndpointResponses,
		},
		{
			name: "Claude client routes to native Claude Messages",
			model: upstreamModel{
				ID: "claude-sonnet-5.5", Vendor: "Anthropic",
				SupportedEndpoints: []string{"/v1/messages"},
			},
			sourceFormat: "claude",
			want:         translate.EndpointMessages,
		},
		{
			name: "Chat client keeps native Chat endpoint for Claude model",
			model: upstreamModel{
				ID: "claude-sonnet-5.5", Vendor: "Anthropic",
				SupportedEndpoints: []string{"/v1/messages", "/chat/completions"},
			},
			sourceFormat: "openai",
			want:         translate.EndpointChatCompletions,
		},
		{
			name: "Responses client routes Gemini fallback to Chat",
			model: upstreamModel{
				ID: "gemini-3.8-flash", Vendor: "Google",
				Capabilities:       modelCapabilities{Family: "gemini"},
				SupportedEndpoints: []string{"/v1/messages", "/chat/completions"},
			},
			sourceFormat: "openai-response",
			want:         translate.EndpointChatCompletions,
		},
		{
			name: "Responses client uses native GPT endpoint",
			model: upstreamModel{
				ID: "gpt-6-luna", Vendor: "OpenAI",
				SupportedEndpoints: []string{"/chat/completions", "/responses"},
			},
			sourceFormat: "openai-response",
			want:         translate.EndpointResponses,
		},
		{
			name: "Responses client prefers Claude Messages over Chat fallback",
			model: upstreamModel{
				ID: "claude-sonnet-5.5", Vendor: "Anthropic",
				Capabilities:       modelCapabilities{Family: "Claude"},
				SupportedEndpoints: []string{"/chat/completions", "/v1/messages"},
			},
			sourceFormat: "openai-response",
			want:         translate.EndpointMessages,
		},
		{
			name: "Claude family metadata selects Messages fallback",
			model: upstreamModel{
				ID: "model-family-only", Vendor: "Other",
				Capabilities:       modelCapabilities{Family: "claude-sonnet"},
				SupportedEndpoints: []string{"/chat/completions", "/v1/messages"},
			},
			sourceFormat: "openai-response",
			want:         translate.EndpointMessages,
		},
		{
			name: "Claude model ID selects Messages fallback",
			model: upstreamModel{
				ID: "claude-sonnet-5.5", Vendor: "Other",
				SupportedEndpoints: []string{"/chat/completions", "/v1/messages"},
			},
			sourceFormat: "openai-response",
			want:         translate.EndpointMessages,
		},
		{
			name: "Responses source preserves native endpoint before Claude fallback",
			model: upstreamModel{
				ID: "claude-sonnet-5.5", Vendor: "Anthropic",
				SupportedEndpoints: []string{"/chat/completions", "/v1/messages", "/responses"},
			},
			sourceFormat: "openai-response",
			want:         translate.EndpointResponses,
		},
		{
			name: "unknown vendor keeps existing Responses fallback order",
			model: upstreamModel{
				ID: "model-unknown", Vendor: "Other",
				SupportedEndpoints: []string{"/chat/completions", "/v1/messages"},
			},
			sourceFormat: "openai-response",
			want:         translate.EndpointChatCompletions,
		},
		{
			name: "unrecognized source keeps existing endpoint order",
			model: upstreamModel{
				ID: "claude-sonnet-5.5", Vendor: "Anthropic",
				SupportedEndpoints: []string{"/chat/completions", "/v1/messages"},
			},
			sourceFormat: "unknown",
			want:         translate.EndpointChatCompletions,
		},
		{
			name:         "unsupported",
			model:        upstreamModel{ID: "embedding-model", SupportedEndpoints: []string{"/embeddings"}},
			sourceFormat: "openai-response",
			wantError:    true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := selectEndpoint(test.model, test.sourceFormat)
			if test.wantError {
				if err == nil {
					t.Fatal("expected an endpoint selection error")
				}
				return
			}
			if err != nil {
				t.Fatalf("select endpoint: %v", err)
			}
			if got != test.want {
				t.Fatalf("endpoint = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeModelsDoesNotInventUnsupportedEndpoints(t *testing.T) {
	t.Parallel()

	models := normalizeModels([]upstreamModel{
		{
			ID:                 "model-a",
			SupportedEndpoints: []string{"/chat/completions"},
			Capabilities: modelCapabilities{
				Supports: modelSupports{Streaming: true, ToolCalls: true, Vision: true},
				Limits:   modelLimits{MaxPromptTokens: 100, MaxOutputTokens: 20},
			},
		},
	})
	if len(models) != 1 || contains(models[0].SupportedEndpoints, translate.EndpointResponses) {
		t.Fatalf("unsupported responses endpoint was invented: %#v", models)
	}
	info := modelInfos(models)[0]
	if contains(info.SupportedGenerationMethods, translate.EndpointResponses) {
		t.Fatalf("model metadata invented responses endpoint: %#v", info.SupportedGenerationMethods)
	}
	if !contains(info.SupportedInputModalities, "IMAGE") {
		t.Fatalf("model metadata omits image support: %#v", info.SupportedInputModalities)
	}
}

func TestFilterModelsExcludesConfiguredPrefixes(t *testing.T) {
	t.Parallel()

	models := filterModels([]upstreamModel{
		{ID: "gpt-5.6-sol"},
		{ID: "claude-sonnet-5"},
		{ID: "Claude-Haiku-4.5"},
	}, []string{"claude-"})
	if len(models) != 1 || models[0].ID != "gpt-5.6-sol" {
		t.Fatalf("filtered models = %#v", models)
	}
}

func TestNormalizeModelPrefixes(t *testing.T) {
	t.Parallel()

	got := normalizeModelPrefixes([]string{" Claude- ", "claude-", "", "GPT-"})
	if len(got) != 2 || got[0] != "claude-" || got[1] != "gpt-" {
		t.Fatalf("normalized prefixes = %#v", got)
	}
}
