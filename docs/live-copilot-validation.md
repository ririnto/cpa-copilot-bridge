# Live Copilot compatibility validation

Validate each model through all three client APIs on a separate, loopback-only CPA host.
Use existing Copilot credentials through the local credential resolver, without changing the operating proxy or global client settings.

| Model | Native Copilot endpoint | Incoming client APIs |
| --- | --- | --- |
| `gemini-3.8-flash` | `/chat/completions` | Chat, Responses, Messages |
| `gpt-6-luna` | `/responses` | Chat, Responses, Messages |
| `claude-haiku-5.5` | `/v1/messages` | Chat, Responses, Messages |

The incoming API selects the client's schema.
The model's configured native endpoint selects the provider schema.
The plugin converts requests and responses between these schemas through the existing translation layer.
An explicit endpoint override can test an exact model missing from discovery, but does not prove that the account supports it.

## Existing CPA conversion

The plugin uses CLIProxyAPI's `sdk/translator/builtin.Registry()` for request, response, and stream conversion.
The pinned SDK does not supply every required conversion pair.
The existing adapters fill those pairs and preserve Copilot reasoning carriers and tool identifiers.
Live validation uses that production path without a separate converter.

Client function tools, including web search and delegated-task functions, retain their names and input schemas through conversion.
Provider-owned native tools have a different execution contract.
The existing filter excludes native tools that the selected endpoint cannot represent and reports exclusions through response metadata and headers.
Forced unsupported tools return an error instead of fabricated tool output.
Ordinary Claude system text remains supported.
The plugin preserves Copilot's structured `model_not_supported` rejection using a fixed message recognized by CPA.
CPA then applies its existing model-specific cooldown while other models retain the credential.

## Acceptance evidence

A successful matrix cell requires an upstream response, the requested model identity, the client's response schema, and nonempty assistant text.
Record HTTP failures separately from successful conversion.
Do not substitute another model to make a cell pass.

Claude Code and Codex client checks must use isolated settings and the same task-owned proxy.
Record web search invocation and completed results separately.
Record subagent invocation and completed delegated work separately.
A prompt requesting a tool does not establish that the tool ran.
Synthetic tests establish translation behavior; they do not establish live provider availability or completed client tools.

Remove temporary authentication files, client settings, host logs, and task processes after validation.
Publish only sanitized summaries.

## Run the opt-in checks

Prepare the platform-specific plugin and CPA v8 host as described by the existing native-host build instructions.
The live checks use the Copilot CLI Keychain credential on macOS.
They require explicit environment flags and do not run during ordinary tests or CI.

```sh
CPA_BINARY=/path/to/prepared/cli-proxy-api \
  CPA_LIVE_COPILOT_MATRIX=1 \
  go test ./integration -run '^TestLiveCopilotProtocolMatrix$' -count=1 -v

CPA_BINARY=/path/to/prepared/cli-proxy-api \
  CPA_LIVE_COPILOT_CLIENTS=1 \
  go test ./integration -run '^TestLiveCLIClients$' -count=1 -v
```

The matrix pins the three native routes listed above, including when discovery advertises multiple endpoints.
It runs nine non-streaming cells and streams only cells whose baseline passes.
The CLI checks allow web search and delegation, then require completed tool events rather than requested tool names.
Each CLI's failed baseline prevents subsequent tool requests for that client.

## Recorded live result

The macOS validation on 2026-10-09 used the authenticated Copilot CLI account with `direct_oauth`.
Discovery returned eight model entries and none of the three exact target IDs.
The isolated host registered all three explicit native-route overrides before the measured matrix started.

| Model | Chat | Responses | Messages |
| --- | --- | --- | --- |
| `gemini-3.8-flash` | FAIL: HTTP 400 | FAIL: HTTP 400 | FAIL: HTTP 400 |
| `gpt-6-luna` | FAIL: HTTP 400 | FAIL: HTTP 400 | FAIL: HTTP 400 |
| `claude-haiku-5.5` | FAIL: HTTP 400 | FAIL: HTTP 400 | FAIL: HTTP 400 |

These nine measured client requests returned the public `invalid_request_error` type.
No baseline produced a successful assistant response, so streaming follow-ups were NOTRUN.
Unknown-model rejection passed, and disabled compaction returned HTTP 422.

Three bounded provider-service diagnostic requests identified `model` as the rejected parameter for each native route.
After request/response body inspection was authorized, three direct native requests returned this same body:

```json
{
  "error": {
    "message": "The requested model is not supported.",
    "code": "model_not_supported",
    "param": "model",
    "type": "invalid_request_error"
  }
}
```

These native requests reject the exact model IDs under the tested credential profile.
They do not establish global model availability.
No aliases or retries were used for these six diagnostic requests.

| Client | Baseline model | Baseline | Web search | Subagent |
| --- | --- | --- | --- | --- |
| Claude Code 2.1.294 | `claude-haiku-5.5` | FAIL: HTTP 400 | NOTRUN | NOTRUN |
| Codex CLI 0.161.0 | `gpt-6-luna` | FAIL: HTTP 400 | NOTRUN | NOTRUN |

Each CLI completed one bounded baseline run and exited with status 1.
Neither emitted a completed assistant answer or a tool call.
Live web-search and delegation acceptance remains outstanding.
The failed live suites retain their failing exit status.

Ordinary Go tests and the CI-selected synthetic native-host suite passed locally.
A native cooldown regression confirms that a repeated unsupported-model request makes only one upstream call and another model remains usable.
A broader supplemental native suite exposed an existing `TestNativeHostResponsesWebsocketCompactionReplay` failure involving instruction preservation.
That supplemental failure remains outside this validation unit.
Temporary client homes, authentication files, host configuration, and host processes were removed after the live checks.
