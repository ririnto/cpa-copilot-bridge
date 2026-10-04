# Engineering Contracts

These contracts describe behavior that clients and the CLIProxyAPI host can observe.

## Provider and Routing

- The plugin owns Copilot authentication, token exchange, model discovery, and upstream execution.
- Select an upstream endpoint from the requested format, model capabilities, and explicit overrides.
- Prefer Responses for Responses clients when the model advertises that endpoint.
- If a Claude-family model has no Responses endpoint, prefer Messages over Chat Completions for Responses clients.
- Preserve native Messages and Chat Completions endpoint preference for their matching clients.
- Keep credentials and cached provider state isolated by auth entry.

## Authentication Refresh

- Renew Copilot API tokens within the configurable expiry buffer.
- On an upstream 401, invalidate the cached Copilot token.
- Retry once for model discovery, JSON execution, and SSE execution.
- Refresh a GitHub OAuth credential near expiry only when it has a refresh token.
- Return rotated GitHub credentials through the host auth callback for normal host persistence.
- Keep configured OAuth permissions unchanged.
- Omit upstream response bodies from authentication errors.
- Preserve HTTP status for host classification.

## Model Discovery

- Build the model list from authenticated Copilot upstream inventory.
- Include entries with absent policy state or `policy.state=enabled`.
- Hide other explicit policy states, `model_picker_enabled=false`, explicit non-chat capability types, and models without a supported chat endpoint.
- Keep missing picker or capability metadata visible for legacy compatibility.
- Block endpoint overrides when policy state is explicitly non-enabled.
- Allow picker-hidden models only through an explicit endpoint override.
- Apply configured excluded prefixes to discovery and default dispatch.
- Return an empty list when valid inventory contains no eligible models.
- Return refresh errors instead of serving the plugin's expired snapshot.
- Do not permanently exclude models because a temporary refresh failed.

## Protocol Preservation

- Preserve native content blocks, block order, and opaque identifiers on same-format routes.
- Translate supported tool-call input across formats and carry opaque reasoning through reversible carriers bound to the Copilot scope.
- Handle only defined block types on each cross-format route.
- Reject non-empty blocks without a target mapping instead of dropping them.
- Restore opaque state only for a matching Copilot scope and a unique assistant or tool-call anchor.
- Reject opaque conversions that cannot retain verification data and tool identifiers the destination cannot represent safely.
- Preflight request, response, and SSE shapes and return errors when conversion would drop content.
- Keep item identifiers, tool-call correlation, encrypted reasoning, signatures, ordering, and provider history intact.
- Translate only across formats and reject a conversion that cannot support a valid next turn.
- Preserve protocol-visible status, usage, error, and stream completion semantics.
- Decode server-sent events by frame boundaries and propagate cancellation through active streams.

## Compaction and Replay

- Preserve native upstream compaction results unchanged.
- Run plugin-managed buffered JSON and Responses SSE compaction only for configured Copilot models.
- Create replay capsules from completed responses and authenticate them before replay.
- Bind compaction capsules to the account credential, API origin, model, and protocol endpoint.
- Replay capsules across host or plugin reloads while the same credential and scope remain available.
- Keep capsule state tied to the bridge's Copilot auth entry because the SDK cannot access a generic selected-auth keyring.
- The plugin cannot compact Codex duplex steer or queue requests because the public SDK exposes no operations for them.
- Bind reasoning replay to the auth entry, model, endpoint, session, and agent.
- Reject malformed, unauthenticated, or out-of-scope replay data.

## Prompt Cache Keys

- Keep bridge-generated Responses cache keys opt-in and separate from CLIProxyAPI's OpenAI-compatible provider setting.
- Preserve an explicit caller key when one exists.
- Otherwise, derive a stable key from the SDK session identity.
- Scope generated keys to the auth entry, model, origin, endpoint, and agent.
- Omit generated keys when the request has no stable session identity.
- Never generate random cache keys.

## Translation Boundaries

- Set `store: false` and request the `reasoning.encrypted_content` include on Claude-to-Responses requests.
- Preserve Claude signatures and distinct tool identifiers through reversible carriers.
- Pass Claude root `output_config.effort` values, including `xhigh` and `max`, to Responses unchanged.
- Map legacy Claude thinking budgets only to `low`, `medium`, or `high` effort.
- Use a supported empty system-message `output_config.effort` marker only when root effort is absent.
- Keep root effort when both the root request and a message provide an effort value.
- Reject effort-marker messages that contain content or unknown fields.
- Map Claude adaptive `thinking.display=updates` to Messages `summarized`.
- Treat `clear_thinking_20251015` with `keep=all` as a no-op and preserve every history block.
- Reject other `keep` edits, unknown context edits, and non-empty server-side safety settings that Copilot cannot enforce.
- Map Claude reasoning settings to Responses `reasoning.effort` and `reasoning.summary=auto`.
- Accept only Responses `encrypted_index` citation annotations when converting to Claude.
- Reject other citation annotations and unknown meaningful output content.
- Use Responses implicit prefix caching for Claude cache hints, regardless of the bridge cache-key setting.
- Strip Claude `cache_control` only from protocol hint locations when translating to Responses.
- Preserve same-named schema and user-data fields during `cache_control` cleanup.
- Let the opt-in `prompt_cache_key` setting attach a stable caller or session root key.
- Omit a generated key when stable session identity is unavailable.
- Responses caching cannot reproduce Claude's exact cache TTL.

## Security and Evidence

- Use fixed provider-error messages and omit upstream response bodies from client errors.
- Keep tokens and private response bodies out of logs.
- Test with synthetic credentials, synthetic payloads, and local mock servers.
- Report mock-provider evidence separately from behavior observed against a live Copilot service.
