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

A successful matrix cell requires the exact upstream request model, a supported response identity, the client's schema, and assistant text.
Record HTTP failures separately from successful conversion.
Do not substitute another model to make a cell pass.

Claude Code and Codex client checks must use isolated settings and the same task-owned proxy.
Record web search invocation and completed results separately.
Record subagent invocation and completed delegated work separately.
A prompt requesting a tool does not establish that the tool ran.
Synthetic tests establish translation behavior; they do not establish live provider availability or completed client tools.

Remove temporary authentication files, client settings, and task processes after validation.
Retain requested debug evidence outside the repository, including failed requests.
Publish only sanitized summaries.

## Run the opt-in checks

Prepare the platform-specific plugin and CPA v8 host as described by the existing native-host build instructions.
Set `CPA_LIVE_COPILOT_AUTH_FILE` to an existing CPA Copilot authentication file to use that credential.
The checks copy its storage into a temporary host and leave the source file unchanged.
File credentials use `token_exchange` by default.
Without a file, the checks use the Copilot CLI Keychain credential on macOS with `direct_oauth`.
They require explicit environment flags and do not run during ordinary tests or CI.

```sh
CPA_BINARY=/path/to/prepared/cli-proxy-api \
  CPA_LIVE_COPILOT_AUTH_FILE=/path/to/copilot-auth.json \
  CPA_LIVE_COPILOT_DEBUG_DIR=/path/to/private-debug-directory \
  CPA_LIVE_COPILOT_MATRIX=1 \
  go test ./integration -run '^TestLiveCopilotProtocolMatrix$' -count=1 -v

CPA_BINARY=/path/to/prepared/cli-proxy-api \
  CPA_LIVE_COPILOT_AUTH_FILE=/path/to/copilot-auth.json \
  CPA_LIVE_COPILOT_DEBUG_DIR=/path/to/private-debug-directory \
  CPA_LIVE_COPILOT_CLIENTS=1 \
  go test ./integration -run '^TestLiveCLIClients$' -count=1 -v

CPA_BINARY=/path/to/prepared/cli-proxy-api \
  CPA_LIVE_COPILOT_AUTH_FILE=/path/to/copilot-auth.json \
  CPA_LIVE_COPILOT_DEBUG_DIR=/path/to/private-debug-directory \
  CPA_LIVE_COPILOT_INCOMPLETE=1 \
  go test ./integration -run '^TestLiveCopilotIncompleteResponses$' -count=1 -v
```

The matrix pins the three native routes listed above, including when discovery advertises multiple endpoints.
It runs nine non-streaming cells and streams only cells whose baseline passes.
The CLI checks allow web search and delegation, then require completed tool events rather than requested tool names.
Each CLI's failed baseline prevents subsequent tool requests for that client.
Debug evidence includes original client and upstream request/response bodies and CLI output.
The checks exclude authentication material and retain evidence in private directories even when validation fails.
Regression fixtures preserve captured JSON fields and stream events, replacing only private or changing values.

## Fresh CPA authentication result

The follow-up validation on 2026-10-09 used a completed CPA Copilot OAuth file with `token_exchange`.
Discovery returned 59 models, including all three exact target IDs and their assigned native endpoints.

| Model | Chat | Responses | Messages |
| --- | --- | --- | --- |
| `gemini-3.8-flash` | PASS | PASS | PASS |
| `gpt-6-luna` | PASS | PASS | PASS |
| `claude-haiku-5.5` | PASS | PASS | PASS |

Both non-streaming and streaming responses passed for these nine combinations: 18 successful requests.
The retained CPA logs confirm all 18 exact outbound model names and native endpoint selections.
Unknown-model rejection and disabled compaction also passed.

Copilot reports native Claude replies as `claude-haiku-5-5` after receiving `claude-haiku-5.5` in the request.
The matrix accepts this observed response spelling for that model only, while preserving the exact request ID.
The plugin leaves the native Claude model field unchanged.

The native Gemini Chat response omitted the `object` field in JSON and streaming chunks.
The plugin now supplies the Chat protocol discriminator while preserving the remaining response fields.
The stream validator accepts the observed final usage-only Chat chunk with empty `choices`.
The Messages fixture uses a 512-token budget because the earlier 64-token budget truncated Gemini's answer.

Claude Code 2.1.294 and Codex CLI 0.161.0 both passed their baseline and completed delegated child work through the proxy.
Claude ran its Agent tool and returned the child answer.
Codex's V2 delegation records its spawn in a persisted rollout, while stdout leaves the wait event's child state empty.
The acceptance check verifies the actual parent spawn/result, linked child session, completed child turn, successful wait, and received child handback.
It rejects assistant claims without those events.

Claude invoked WebSearch, but Copilot rejected the native tool with HTTP 400:

```json
{"error":{"message":"The use of the web search tool is not supported.","code":"unsupported_value"}}
```

The plugin preserves this known rejection as a structured client error without exposing arbitrary upstream error text.
This is an invoked but unsupported search, not a completed search.

| Client | Baseline | Web search | Subagent |
| --- | --- | --- | --- |
| Claude Code 2.1.294 | PASS | Invoked; unsupported HTTP 400 | PASS |
| Codex CLI 0.161.0 | PASS | PASS | PASS |

The final affected search rerun passed Codex's strict started/completed ID correlation and returned a source URL.
Both client baselines passed again with the rebuilt plugin.
The aggregate search command exited with status 1 because Claude's unsupported native search remains a failed acceptance case.

Codex's isolated profile copies its official model catalogue entry and changes only `use_responses_lite` and `tool_mode` to enable the full Responses API and direct tools.
The temporary client home supplies the catalogue through the supported `model_catalog_json` setting.
The isolated Claude invocation enables WebSearch and Agent explicitly.

Native Copilot hosted search changes its opaque item ID between stream phases and in the completed snapshot.
The plugin buffers the ordered stream from the first hosted search until a valid terminal snapshot, bounded at 8 MiB.
It retains the final snapshot and replay data, and uses its native ID for earlier search lifecycle events.
Later text and tool events wait behind this buffer.
Valid `response.incomplete` snapshots retain their partial output, reason, usage, and actual search status.
Missing terminal snapshots or inconsistent search lifecycle data return an error.

## Review regression checks

The follow-up review identified five cases covered by regression tests:

- OAuth rotation followed by a failed account lookup retains the new token pair in pending storage. The host retries verification after restart; only a verified account can activate the pair, and the previous refresh preference is restored afterward.
- Legacy replay migration retains the old credential fingerprint and account/auth binding when endpoint discovery is temporarily unavailable. Existing capsule authentication must prove the original endpoint; endpoint, account, or auth-ID changes cannot replay the artifact.
- Native incomplete Responses streams retain the terminal event and partial output without caching a successful replay. Chat and Messages map supported token-limit/content-filter reasons through their existing finalization paths. The native host test covers all three client schemas.
- Native tool declarations that the pinned SDK cannot represent use the existing exclusion metadata and headers. Forced excluded tools fail explicitly; supported function, custom, namespace, Claude web search, and local Chat shell declarations retain their existing mappings.
- Reasoning-only assistant carriers and mixed text/tool turns preserve reasoning before visible output at the original conversation position, using the existing SDK conversion path and tool identifiers.

The affected live token-limit probe returned `response.incomplete` with `max_output_tokens`, 24 input tokens, and 16 output tokens, all used for reasoning.
Its four captured request/response bodies retain the original fields and five stream events in sanitized regression fixtures.
HTTP/SSE native Responses, Chat, and Messages partial termination is covered; incomplete tool arguments that cannot be represented by the destination fail explicitly.

Regression fixtures include the corresponding client request, upstream request, upstream response, and client response bodies.
Private values use placeholders while the protocol fields and events remain intact.
CPA's formatted SSE logs required reassembly at transport chunk boundaries; one lost instruction space was restored from the paired client response.
Fixture notices record this reconstruction. Original diagnostics remain private and unchanged.

## CPA v8.0.23 compatibility follow-up

The current host and SDK are pinned to v8.0.23. Existing CPA handling now cancels an active plugin HTTP stream on `response.interrupt`, emits `response.incomplete` with reason `interrupted`, and permits the next request on the same client WebSocket. Native Codex WebSocket sessions forward the original interrupt body unchanged and retain continuation identity. These paths have required synthetic native-host regressions. After the completed plugin HTTP forwarder has exited, a late interrupt still returns HTTP 400; the native Codex completed-session handling does not extend to the plugin executor.

Codex Responses requests can declare optional `web_search` with `external_web_access: false`. When the selected endpoint is Claude Messages, the existing native-tool exclusion mechanism now removes this unrepresentable declaration and discloses it in `X-Copilot-Excluded-Native-Tools` and response metadata. Ordinary function tools remain intact. Forced selection of the excluded tool fails before inference. This does not implement cached-only search or change it to live web search.

Original Codex 0.161.0 PNG and PDF requests were rerun with full private body captures. PNG still fails before inference because its `detail: high` has no verified lossless Claude Messages mapping. The PDF initial request now reaches the provider and returns an actual rendering function call; The existing strict cancellation proof now permits continuation after a fully forwarded function-call terminal while retaining the read-cancellation record. A subsequent original renderer attempt failed because nested Seatbelt could not launch shell commands. An alternate isolated `danger-full-access` attempt avoided nesting but changed the generated search declaration to `external_web_access=true`, even with an explicit cached-search setting; Copilot refused it with HTTP 400. Those failures and every physical call remain retained. The maintained packet keeps the original `workspace-write` profile and renderer/view tools. The installed CLI rejects an external-sandbox mode; the distinct app-server policy was not substituted for the original PDF CLI route. Full PDF rendering and attachment completion remain unverified. An isolated original app-server run through the real proxy also confirms that `turn/interrupt` ends an active turn and permits another turn in the same thread, but this client version closes its WebSocket without emitting `response.interrupt`. Synthetic wire-control coverage must not be presented as an original-client interrupt frame.

## Historical WebSocket interrupt limitation

The exact Codex `response.interrupt` frame is rejected by CPA before it reaches the plugin.
Native diagnostics reproduce this for both plugin normalization and active upstream WebSocket duplex processing.
The examined CPA v8.0.15 and v8.0.20 handlers do not implement this control message, and the plugin executor has no downstream WebSocket control hook.
The error is therefore not resolved by this plugin change.
The pinned host's Responses WebSocket forwarder also treats `response.incomplete` as an unfinished stream: it forwards the event, logs a completion error, and closes the connection instead of preserving continuation.
Incomplete response support in this validation applies to HTTP/SSE; the plugin does not fabricate a `response.completed` event to satisfy the host's WebSocket completion check.

The isolated Codex profile uses supported settings to select HTTP transport and disable immediate interruption:

```toml
[features]
instant_interrupt = false

[model_providers.copilot_proxy]
supports_websockets = false
```

Use the actual custom provider name when applying the transport setting.
No operating proxy or global client configuration was changed during validation.

## Earlier Keychain result

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
Web-search and delegation were not exercised under this earlier credential profile; fresh CPA authentication results are recorded above.
The failed live suites retain their failing exit status.

Ordinary Go tests and the CI-selected synthetic native-host suite passed locally.
A native cooldown regression confirms that a repeated unsupported-model request makes only one upstream call and another model remains usable.
A broader supplemental native suite exposed an existing `TestNativeHostResponsesWebsocketCompactionReplay` failure involving instruction preservation.
That supplemental failure remains outside this validation unit.
Temporary client homes, authentication files, host configuration, and host processes were removed after the live checks.

The follow-up recorded 11 additional inference, six authentication and six catalogue dispatches, bringing expanded totals to 38, 20 and 16. One alternate-profile retry occurred before the intended cached-setting edit matched the generated config; its repeated 400 and a separate private correction record are retained. These are measured counts, not limits.
