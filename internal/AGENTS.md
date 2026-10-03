# Internal Packages

- Keep provider execution and provider state in `internal/provider`.
- Keep protocol conversion in `internal/translate` and stream framing in `internal/sse`.
- Apply [engineering contracts](../docs/engineering-contracts.md) when changing protocol or runtime behavior.
- Preserve native payload fields, item identifiers, tool-call identifiers, encrypted reasoning, and signed thinking on same-format paths.
- Translate only when the client and provider formats differ.
- Reject conversions that cannot preserve the data required for a valid follow-up turn.
- Scope tokens, model caches, OAuth sessions, and reasoning replay to the correct auth entry and request context.
- Clear derived state when a configuration change makes it stale.
- Keep compatibility compaction opt-in and authenticate replay data against its configured scope.
- Propagate cancellation and close response bodies and streams when their work ends.
- Redact credentials and private upstream data before returning errors or writing logs.
