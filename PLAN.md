# Copilot Bridge Delivery Plan

## Outcome

Build a native CLIProxyAPI v8 plugin that preserves Copilot protocol state across agent turns.
Support GitHub device login, model discovery, native Responses, Chat Completions, and Claude Messages.
Preserve upstream item IDs, tool correlation IDs, opaque reasoning, and signed thinking.
Provide opt-in Codex summary compaction with authenticated replay when the upstream lacks native compaction.

## Scope

The project owns its source, tests, documentation, configuration examples, and local build artifacts.
The existing CLIProxyAPI checkout and running proxy configuration are reference inputs.
Remote publication, production installation, and account login are outside this delivery.

## Design

Start from the MIT-licensed Copilot provider plugin v0.3.3.
Use the official v8 SDK and native C ABI.
Own upstream execution to avoid Codex-specific ID and encrypted-content sanitizers.
Choose endpoints from provider model capabilities and explicit protocol overrides.
Preserve native same-format traffic and use scoped translation for cross-format traffic.
Keep reasoning replay scoped to endpoint, model, credential, and conversation history.

## Owners

Main owns integration, configuration, build tooling, commits, documentation, and acceptance evidence.
Delegates inspect the existing plugin, SDK hook contracts, and client protocol requirements.
Implementation assignments give each package one writer.

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
- Protocol and client contract investigations are in progress.
