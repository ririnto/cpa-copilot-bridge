# Copilot Bridge Delivery Plan

## Outcome

Build a native CLIProxyAPI v8 plugin that preserves Copilot protocol state across agent turns.
Support GitHub device login, model discovery, native Responses, Chat Completions, and Claude Messages.
Preserve upstream item IDs, tool correlation IDs, opaque reasoning, and signed thinking.
Provide opt-in Codex summary compaction with authenticated replay when the upstream lacks native compaction.

## Scope

The project owns its source, tests, documentation, configuration examples, and local build artifacts.
The existing CLIProxyAPI checkout and running proxy configuration are reference inputs.
Private repository publication, issue and pull request delivery, and merging into `main` are authorized.
An isolated account login is authorized and keeps auth storage outside the checkout.
Production installation remains outside this delivery.

## Design

Start from the MIT-licensed Copilot provider plugin v0.3.3.
Use the official v8 SDK and native C ABI.
Own upstream execution to avoid Codex-specific ID and encrypted-content sanitizers.
Choose endpoints from provider model capabilities and explicit protocol overrides.
Preserve native same-format traffic and use scoped translation for cross-format traffic.
Keep reasoning replay scoped to endpoint, model, credential, session, agent, and exact tool calls.

## Owners

Main owns integration, configuration, build tooling, commits, and acceptance evidence.
The documentation owner maintains README and engineering contracts during assigned work.
Delegates inspect the existing plugin, SDK hook contracts, and client protocol requirements.
Implementation assignments give each package one writer.
The working branch is `codex/copilot-protocol-compat` and the target branch is `main`.
Use current branch changes and upstream release tags as durable review references.

## Delivery Units

1. Import the licensed provider baseline and adapt it to CLIProxyAPI v8.
2. Preserve protocol state and fix stream and history compatibility.
3. Add optional compaction compatibility and replay validation.
4. Validate the native artifact with an isolated CLIProxyAPI host and mock upstream.
5. Complete independent review, installation instructions, and delivery evidence.

## Acceptance Evidence

Run existing and added package tests, race checks, vet, and native shared-library builds.
Test long and special-character IDs without changing native Copilot item IDs.
Test opaque encrypted reasoning, signature-only thinking, terminal-only reasoning, and subsequent replay.
Test cancellation, partial SSE frames, errors, token refresh, and credential isolation.
Test compaction completion, authenticated replay, invalid capsules, and disabled behavior.
Load the built library in an isolated current CLIProxyAPI host and send synthetic requests to a local mock upstream.
Report live-provider behavior as unverified unless a real provider call is explicitly exercised.

## Progress

- Imported upstream v0.3.3, preserving its MIT license.
- Adapted module and SDK references to CLIProxyAPI v8.0.13.
- Implemented native protocol preservation, reversible Claude carriers, scoped reasoning replay, and optional compaction.
- Replaced Makefile tooling with module-managed Task and set Go 1.26.8 as the supported minimum.
- Verified native loading and Responses, Messages, and Chat Completions requests with a disposable v8 host.
- Completed GitHub device login through the isolated proxy with private auth storage outside the checkout.
- Passed formatting, race tests, vet, and native builds on Go 1.26.8.
- Passed eight native host integration cases, including missing-ID restoration, cancellation, and both compaction routes.
- Completed independent pull request review with no confirmed blockers.
- Recorded downstream tool identifier limits as a tracked follow-up.
- Merged the initial delivery into `main` and tagged `v0.1.0`.

## PR Compatibility and Live Verification

Review upstream prompt-cache and compaction changes against the public plugin SDK.
Apply feasible behavior without changing upstream PR worktrees.
Document SDK limits instead of claiming unsupported duplex or provider parity.
Use the existing Copilot credential in an isolated host for synthetic live requests.
Verify reasoning and tool-history replay for advertised native protocols.
Test `gemini-3.8-flash` calls, repeated-prefix caching, and available thinking fields.
Keep live auth, request bodies, response bodies, and opaque state outside publications.
Check existing catalog-plus-partial-override operation in the separate catalog project.

Main owns the plan, operator configuration, live calls, commits, and delivery.
Read-only delegates compare each upstream PR, SDK hooks, client contracts, and privacy behavior.
Implementation assignments name owned files before editing.
The working branch is `codex/pr-parity-live-verification` and the target branch is `main`.
Use package checks, native mock integration, metadata-only live results, and one independent published review as acceptance evidence.

## Six Client Routes

Both Claude Messages clients and Codex Responses clients must work with each advertised Copilot protocol.
Cover Chat Completions with Gemini 3.8 Flash, Responses with GPT-6 Luna, and Messages with Claude Sonnet 5.5.
Preserve all supported content blocks, original protocol identifiers, opaque thinking, and tool correlation.
Test JSON and SSE output, tool-result replay, and subsequent conversation turns.
Use reversible opaque carriers at format boundaries and keep unknown carrier conversions fail-closed.
Check actual client execution separately from synthetic protocol requests.

The translator owner implements coupled carrier and dispatch changes.
The integration owner verifies all six routes with synthetic upstream fixtures.
Main owns private live checks and client configuration.
The documentation owner records confirmed protocol guarantees and public SDK limits.

## Verification Findings

The first live matrix passed ten of twelve JSON and SSE combinations.
Both clients passed Gemini opaque-state replay, tool results, and a third conversation turn.
Copilot Responses rejected Anthropic cache controls on content blocks.
Copilot Messages rejected message-level effort markers, thinking display updates, and server safeguard requests.
Normalize supported request options while retaining content blocks and signed or encrypted state.
Reject unsupported server controls and meaningful blocks before forwarding.
Run Claude Code with local manual permission checks for Copilot compatibility.
Expand authenticated compaction capsules before ordinary Responses SSE replay.
Repeat affected package checks, the native host matrix, and all six live client routes after these fixes.
Publish the coupled unit after Main verifies the required acceptance evidence.
Run one independent review of the published changes before merging into main.

## Available Model Exposure

Expose only eligible inference models from the authenticated Copilot inventory.
Exclude explicit policy denials, hidden internal models, and unsupported non-chat endpoints.
Keep absent optional discovery fields compatible with older upstream inventories.
Endpoint overrides must not bypass explicit account policy denials.
Check other provider catalog boundaries against authoritative discovery without inventing availability.
The provider owner implements filtering with synthetic inventory fixtures.
Main verifies the native host model list and retains prior live evidence for unchanged execution paths.

## Current Acceptance

Passed Go 1.26.8 package race checks, formatting checks, vet, and the native shared-library build.
Passed the full native host suite and all twelve protocol matrix combinations.
The matrix verifies signed and redacted reasoning, original identifiers, tool-result correlation, and complete SSE framing.
Passed live JSON and SSE tool-result replay and third-turn checks for all six client routes.
Confirmed non-empty opaque reasoning replay separately when a short tool prompt returned no reasoning.
Passed buffered JSON and SSE compaction followed by authenticated capsule replay.
Confirmed the host exposes only eligible authenticated model IDs and configured aliases.
Rendered the README Mermaid source to SVG and PNG and inspected the PNG.
Raw runtime evidence and authentication remain outside repository publications.

## Published Review Fixes

Reject cross-format `previous_response_id` references because Chat and Messages cannot resolve Responses server-side context.
Keep native Responses references unchanged and require full history for cross-format replay.
Authenticate Chat opaque carriers at the selected provider credential boundary before accepting replay.
Bind the wrapper to the auth entry, credential, API origin, model, and endpoint.
Repeat affected package, native host, and live Chat replay checks before the reviewer reassesses these changes.
Passed the final Go 1.26.8 check and all native host protocol cases after both fixes.
Passed all four live Chat JSON and SSE replay combinations with authenticated carriers.
Passed installed Claude Code and Codex tool execution and resume on the affected Chat model.
Reject an API-origin transition during a model retry before sending the prepared history to the new origin.
Keep same-origin token renewal and retry behavior intact.
Validate typed thinking signatures regardless of message role before forwarding.

## Remaining State and SDK Delivery

Resolve the tracked identifier, compaction lifecycle, and credential continuity limits.
Main owns Git delivery, SDK dependency selection, private live checks, and the shared plan.
Use `codex/plugin-state-lifecycle` against `main` for the bridge.
Use `codex/plugin-lifecycle-sdk` against `main` in the maintained host fork when SDK changes are required.
Use the named upstream `v8.0.15` release as the host baseline.
Preserve upstream history and existing operator configuration.

The provider owner designs stable authenticated state across legitimate OAuth token renewal.
The translator owner handles bounded tool identifiers with exact replay correlation.
The host owner adds selected-auth and duplex lifecycle seams required by supported compaction.
The integration owner verifies coupled native plugin and host behavior after interfaces settle.
Independent reviewers inspect each published coherent delivery unit.

Test cancellation, queued turn preservation, selected-auth isolation, restart continuity, and changed-account rejection.
Run affected deterministic checks and native host integration without post-connect timeouts.
Repeat affected authenticated client routes and inspect metadata-only evidence for token renewal and replay.
Keep auth material and provider bodies outside commits and publication.
Record exact checks and merge each accepted unit into its authorized `main`.

### Current Decisions and Evidence

Upgraded both plugin SDK dependencies from `v8.0.13` to upstream `v8.0.15`.
Built the latest upstream host with Go 1.26.8.
Fast-forwarded the isolated maintained fork to the existing native compaction branch without rewriting authorship.
Passed focused native compaction checks before applying selected-auth isolation corrections.
Keep generic and native Codex duplex compaction in the host because Copilot HTTP cannot receive in-flight websocket steering.
Reuse the existing scoped host HTTP callback for catalog cancellation with explicit bounded transport capability.
Persist bridge continuity keys in provider auth storage and retain only verified current-credential legacy migration keys.
Main captured live legacy Chat reasoning and Responses compaction state for post-migration replay checks.
Keep temporary SDK workspaces and private migration evidence outside Git.

### Final Lifecycle Acceptance

Use the maintained host release `v8.0.15-cpa.1` through a portable Go module replacement.
The host keeps native Codex and generic Responses compaction, including queued WebSocket turns.
The bridge keeps Copilot HTTP execution and authenticated reasoning replay.
OAuth continuity uses a persisted random key bound to the verified account.
Static API credentials retain credential-bound isolation.
Reject unsupported foreign Chat reasoning before dispatch instead of substituting a placeholder.
Reject Responses item IDs and tool call IDs above the observed 64-character upstream limit.
Preserve valid original IDs and decode longer transport carriers before native dispatch.

Passed all twelve authenticated JSON and SSE protocol combinations with tool replay and a third turn.
Passed all six routes using 64-character original IDs and a 203-character reversible carrier.
Passed installed Codex 0.160.0 and Claude Code 2.1.289 tool execution and session resume.
Clarified the Claude fixture prompt to exclude line numbers after one model copied tool formatting.
Passed live legacy thinking and compaction replay after keyring migration and a host restart.
Passed controlled API bearer expiry with real Copilot responses `200`, `401`, and `200`.
The bridge acquired a new API token and retried the model request successfully.
The current GitHub login has no OAuth refresh grant, so grant rotation uses deterministic authenticated fixtures.
Passed buffered and SSE compaction followed by capsule replay.
The README Mermaid source still matches the successfully rendered and inspected diagram.
