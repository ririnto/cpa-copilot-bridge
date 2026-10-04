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
