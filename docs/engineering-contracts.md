# Engineering Contracts

These contracts describe behavior that clients and the CLIProxyAPI host can observe.

## Provider and Routing

- The plugin owns Copilot authentication, token exchange, model discovery, and upstream execution.
- Select an upstream endpoint from the requested format, model capabilities, and explicit overrides.
- Keep credentials and cached provider state isolated by auth entry.

## Protocol Preservation

- Preserve native protocol requests and responses when client and upstream formats match.
- Keep item identifiers, tool-call correlation, encrypted reasoning, signatures, ordering, and provider history intact.
- Translate only across formats and reject a conversion that cannot support a valid next turn.
- Preserve protocol-visible status, usage, error, and stream completion semantics.
- Decode server-sent events by frame boundaries and propagate cancellation through active streams.

## Compaction and Replay

- Preserve native upstream compaction results unchanged.
- Run plugin-managed compaction only when configuration enables it.
- Create replay capsules from completed responses and authenticate them before replay.
- Bind compaction capsules to the account credential, API origin, model, and protocol endpoint.
- Bind reasoning replay to the auth entry, model, endpoint, session, and agent.
- Reject malformed, unauthenticated, or out-of-scope replay data.

## Security and Evidence

- Keep tokens and private response bodies out of logs and user-visible errors.
- Test with synthetic credentials, synthetic payloads, and local mock servers.
- Report mock-provider evidence separately from behavior observed against a live Copilot service.
