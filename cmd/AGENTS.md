# Command and Host ABI

- Keep `cmd/cpa-copilot-bridge` as the CLIProxyAPI host adapter and method dispatcher.
- Keep provider behavior and protocol conversion in `internal` packages.
- Use `sdk/pluginabi` and `sdk/pluginapi` as the source of truth for host methods, ABI versions, and shared types.
- Treat exported C symbols, C struct layouts, JSON field names, envelopes, and buffer ownership as host contracts.
- Follow the SDK allocation and release rules for every host and plugin buffer.
- Do not retain borrowed host buffers after a callback returns.
- Keep ABI errors free of credentials, machine paths, and raw upstream response bodies.
- Pair host ABI changes with integration coverage through an isolated CLIProxyAPI process.
