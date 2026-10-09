# Hosted server tools and destination-native requests

Expanded acceptance is tracked in [issue #5](https://github.com/ririnto/cpa-copilot-bridge/issues/5).
The earlier [live validation](live-copilot-validation.md) remains complete within its documented scope.
Its 18 JSON/SSE inference cells do not establish hosted tools or complete native-client request parity.
Unsupported responses, excluded declarations, client-executed functions, and unrun cases do not satisfy this acceptance.

## Assigned destinations

| Exact model | Destination | Existing hosted-tool evidence |
| --- | --- | --- |
| `gemini-3.8-flash` | Chat | Provider-executed server tools remain unproven. |
| `gpt-6-luna` | Responses | Native hosted web search completed with correlated events and citations. |
| `claude-haiku-5.5` | Messages | Native WebSearch returned HTTP 400 with `unsupported_value`. |

The captured GPT search action includes `query` and `queries`, without full results or action sources.
Citations establish cited sources, but do not establish complete result content or replay state.
Public API documentation describes schemas and does not prove capabilities of an assigned Copilot endpoint.
Existing configured aliases may resolve to the assigned exact upstream model without changing that model.
No model substitution, invented alias, hidden reroute, fabricated result, or invented provider replay token is permitted.

## Acceptance dimensions

Each supported family requires preserved declarations, selection, request options, provider call identity, actual results, and content.
JSON and SSE must retain relevant citations, usage, event ordering, terminal outcomes, and errors.
Continuation must retain genuine history, paired results, replay tokens, and ownership relationships.
Provider-internal tool failures inside successful HTTP responses remain distinct from HTTP-level refusal.
A valid incomplete response must not become a fabricated completed response.

The known conversion gaps include Chat search options, hosted declarations, hosted output, and result-only continuation.
Messages server calls must retain provider execution semantics when translated to Responses.
They must not become executable client function calls.
Responses search must retain meaningful result content and identity when translated to Messages or Chat.
The existing SDK conversion routes require guards against empty synthesized results and invented identifiers.

## Request parity

Actual installed Copilot CLI default-provider captures govern Copilot upstream HTTP headers and body behavior.
BYOK requests and VS Code source alone do not establish that baseline.
Destination-client behavior remains an additional comparison for incoming Codex and Claude requests.
Compare the same assigned provider, exact model, and user task against genuine current native-client baselines.
Assess method, scheme, authority, path/query, header semantics, required values, body schema/content, encoding, and streaming separately.
Preserve ordered instructions, executable tool schemas, selection, parallelism, effort, output limits, structured output, and continuation state.
Provider-native preparation applies by destination even when the incoming client uses another protocol.
Copied destination tool names require actual adapters to tools available in the incoming client.

Random identifiers and timestamps may differ across independent runs.
Session, thread, cache, call, and replay identifiers require equivalent binding, lifetime, and equality relationships.
Instruction content, tool behavior, header presence, effort, limits, and encoding choices cannot be normalized away.
Credentials remain local and excluded from published evidence.
A successful status or matching protocol label alone does not establish parity.

## Reference scope

The pinned CLIProxyAPI translator registry supplies reusable conversion routes.
Its native Claude and Codex executor preparation resides in internal packages unavailable through the external plugin SDK.
Those paths provide implementation guidance without authorizing direct Anthropic or ChatGPT credentials on Copilot requests.
See [Claude preparation](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.15/internal/runtime/executor/claude_executor_request.go) and [Codex preparation](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.15/internal/runtime/executor/codex_executor_request.go).

[OpenCodex routing](https://github.com/lidge-jun/opencodex/pull/3866) assigns GPT Responses destinations independently of incoming format.
Its [bearer/origin snapshot change](https://github.com/lidge-jun/opencodex/pull/2841) reinforces credential-bound dispatch.
Its [Responses repair](https://github.com/lidge-jun/opencodex/blob/main/src/server/github-copilot-responses-repair.ts) corroborates provider identifier and completion concerns.
Existing bridge captures remain the authority for its observed Copilot behavior.

VS Code supplies maintained Copilot proxy request construction as provisional implementation guidance.
Any provider header changes derived from this source require reconciliation with actual installed Copilot CLI captures.
Its [Claude proxy](https://github.com/microsoft/vscode/blob/main/src/vs/platform/agentHost/node/claude/claudeProxyService.ts) forwards version, supported beta families, and transformed user-agent semantics.
Its [Copilot service](https://github.com/microsoft/vscode/blob/main/src/vs/platform/agentHost/node/shared/copilotApiService.ts) distinguishes Messages proxy intent from Responses conversation intent.
Its [initiator correction](https://github.com/microsoft/vscode/pull/320343) omits origin classification when the genuine turn origin is unavailable.
Its [Codex proxy](https://github.com/microsoft/vscode/blob/main/src/vs/platform/agentHost/node/codex/codexProxyService.ts) does not justify forwarding every inbound header.

The supplied Codex, ZCode, Copilot CLI, GitHub App, and VS Code repositories were inspected read-only.
Codex [request construction](https://github.com/openai/codex/blob/main/codex-rs/core/src/client.rs) informs current body and stream comparisons.
Its [compression discussion](https://github.com/openai/codex/issues/41662) distinguishes official authenticated-provider compression from custom-provider behavior.
Copilot CLI and App checkouts contain release/documentation material rather than authoritative inference request implementation.
They cannot supply missing wire-level baselines.

The bounded discussion inventory covered CLIProxyAPI issues/PRs 5871, 5661, 5735, 6392, 3837, 4094, and 3670.
It covered OpenCodex issues/PRs 3866, 2841, 1110, 1111, and 6472.
It covered VS Code 320343 and 298683, Codex 41662, Copilot CLI 4594, and Copilot App 2733.
The [unmerged Copilot proposal](https://github.com/router-for-me/CLIProxyAPI/pull/5661) and [unmerged search proposal](https://github.com/router-for-me/CLIProxyAPI/pull/3837) are historical guidance, not shipped support.
The [open forwarding proposal](https://github.com/router-for-me/CLIProxyAPI/pull/4094) does not authorize hidden model rerouting.
The [HTTP continuation discussion](https://github.com/router-for-me/CLIProxyAPI/issues/5735) and [related limitation](https://github.com/router-for-me/CLIProxyAPI/issues/6392) remain relevant host boundaries.

## Attachments

Representative inputs are a valid PNG and a small PDF with verifiable content.
Each attachment follows the original client's preparation, file-reading tools, and follow-up requests.
Native Claude sends inline PDF content, while native GPT and Gemini initially send local tagged-file references.
Forcing an inline PDF would not validate the latter clients' original paths.
Equivalent protocol declarations must retain identical decoded bytes, MIME, reference semantics, and representable metadata.
Native Copilot captures establish which declarations and exact models accept these inputs.
Synthetic converter tests, sanitized captured fixtures, and real provider acceptance are reported separately.
Uploads, remote URLs, office files, audio, and video remain unproven without applicable captures and allocated calls.
JSON/SSE responses and continuations must retain relevant attachment content, results, citations, and provider-owned references.
A rejection, dropped part, or unrun case keeps the corresponding acceptance open.
The representative allocation does not establish a complete attachment matrix.

## Recorded execution limits

The initial new execution ceiling is 44 physical inference dispatches, without automatic retries or phase budget transfers.
Authentication and catalogue dispatches have separate ceilings of eight and four.
A task-owned forwarding gate must reject excess requests before upstream dispatch.
Failures count against the budget and retain private bodies and sanitized HTTP metadata.

| Phase | Ceiling | Scope |
| --- | ---: | --- |
| A | 8 | Current native Codex and Claude search baselines receive four slots each. |
| B | 5 | Five native family capability candidates receive one slot each. |
| C | 9 | Six native Copilot PNG/PDF representatives precede three conditional translated search or continuation calls. |
| D | 8 | Cross-client search parity receives four slots for each destination. |
| E | 2 | Changed-evidence verification or confirmed cross-protocol attachment checks receive at most two calls. |
| F | 12 | Native GPT and Gemini PDF paths receive four slots each, followed by four conditional original-path cross-client attachment slots. |

Phase B candidates are Gemini Chat search, GPT Responses source-bearing search, Claude fetch, Claude code execution, and GPT code interpreter.
Only genuine successful native execution permits its translated-family claims.
Additional successful families require a recorded family matrix before cross-protocol support is claimed.
Image generation, file search, hosted MCP, and tool search remain unknown or unrun without established capability and required real resources.
Client-executed shell, computer, and ordinary functions are tracked separately.

The verified retained historical native-host logs contain 56 physical inference requests.
They contain 24 GPT Responses calls, 22 Claude Messages calls, and 10 Gemini Chat calls.
This is not a complete historical total and excludes authentication, catalogue, and other direct captures.
Fresh outcomes, dispatch counts, and unresolved cells must be recorded before expanded acceptance is closed.

Phase F was revised before its first dispatch to follow each original client's complete attachment path.
The total and phase ceilings remain unchanged.
Catalogue capacity is exhausted after four captured native catalogue requests.
Any explicit local catalogue replay must preserve genuine captured bytes and verify credential, origin, client-profile, and response-hash bindings.
Local replay is recorded separately and does not establish a fresh catalogue response.

## Current native evidence

Eleven native inference requests completed, with seven authentication dispatches and four catalogue dispatches recorded.
The authentication count includes one accidental credential-free test request that returned HTTP 404.
Subsequent offline tests enforce loopback-only isolation.
Native Claude PNG and PDF content assertions passed, as did native Gemini PNG content.
Native GPT PNG returned HTTP 200 with unchanged input bytes but failed the visual-content assertion.
The initial GPT and Gemini PDF runs prohibited tools and therefore establish only tagged-file preparation.
They do not establish PDF support failure or completion of the original file-reading path.
Two further GPT requests establish the native `view` call, paired PDF result, and completed marker answer.
Three further Gemini requests establish the same path and a completed same-session user-turn continuation.
The native reader returned the original PDF file text without a custom extraction step.
These bounded runs expose the original native `view` tool and preserve native temporary-file preparation.
GPT's extra user turn remains unrun because the temporary inspector initially assumed an unavailable ACP tool-name field.
Its corrected offline replay correlates the actual upstream call and result without repeating inference.
Five additional request/SSE fixtures preserve these real call identities, file results, and histories after sanitization.
This evidence does not establish arbitrary PDF interpretation or cross-client attachment parity.
HTTP/1.1 no-replay gate checks passed, without establishing full HTTP/2 or TLS-framing parity.
Actual native profiles differ from the provisional provider-header change, which remains unpublished pending reconciliation.
