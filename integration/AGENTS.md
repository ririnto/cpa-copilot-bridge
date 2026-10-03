# Integration Tests

- Use a disposable CLIProxyAPI host and a local `httptest` upstream for host-level tests.
- Create test credentials, configuration, logs, and plugin artifacts under `t.TempDir()`.
- Use fake tokens and synthetic protocol payloads in every fixture.
- Do not read user configuration or auth directories, contact GitHub, or call a live provider.
- Assert observable host requests, provider routing, response fields, and stream events.
- Synchronize on readiness or protocol events instead of `time.Sleep`.
- Keep failure output free of real credentials, user paths, and copied provider payloads.
