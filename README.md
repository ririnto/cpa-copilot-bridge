# Copilot Bridge

A native CLIProxyAPI v8 plugin for GitHub Copilot subscription models.
The plugin handles device login, Copilot token refresh, model discovery, and upstream execution.
It derives from the MIT-licensed Copilot plugin [v0.3.3](https://github.com/arthur-sommer-etc/cliproxyapi-copilot-plugin/tree/v0.3.3).
See [NOTICE.md](NOTICE.md) for attribution.

## Requirements

- Go 1.26.8 or newer and a C compiler for the native shared library.
- CLIProxyAPI v8 with its native plugin loader enabled.
- A GitHub account with access to Copilot subscription models.

The module uses CLIProxyAPI SDK v8.0.13.
Build the host and plugin for the same operating system and architecture.

## Build and Install

```bash
go tool task check
```

The native artifact is written to `build/plugins/<os>/<arch>/cpa-copilot-bridge.<ext>`.
The extension is `dylib` on macOS, `so` on Linux, and `dll` on Windows.
Copy the artifact into `<plugin-root>/<os>/<arch>/` and configure the host using [config.example.yaml](config.example.yaml).
Restart the host after installing a new native library.
The plugin identifier is `copilot-bridge`.
The configuration key is the library basename, `cpa-copilot-bridge`.

```yaml
plugins:
  enabled: true
  dir: ./plugins
  configs:
    cpa-copilot-bridge:
      enabled: true
      reasoning_replay: true
      prompt_cache_key: false
      compaction_models: []
      model_endpoint_overrides: {}
```

Keep authentication files in a private directory outside the checkout.
Protect the host management endpoint with its configured management key.

## Login

Use the host OAuth management API with `provider=copilot-bridge`.
The plugin starts GitHub device authorization and exchanges the approved credential for a Copilot API token.
The host stores the GitHub credential through its normal auth storage.
The plugin refreshes short-lived Copilot tokens in memory.

```text
GET /v8/management/oauth/auth-url?provider=copilot-bridge
GET /v8/management/oauth/status?state=<returned-state>
```

Open the returned GitHub device URL, approve access, and poll until the status is `ok`.
Send your host client API key to inference endpoints.
Do not send the GitHub credential to clients.

## Protocol Behavior

Native `/responses`, `/v1/messages`, and `/chat/completions` paths retain provider state.
The plugin chooses an advertised endpoint matching the client format when available.
An exact model override can select an endpoint when discovery metadata is incomplete.

Responses item IDs, tool `call_id` values, encrypted reasoning, and previous-response references remain distinct.
Claude signed thinking and redacted thinking remain intact on the native Messages path.
The Claude-to-Responses bridge carries signatures and separate tool identifiers through reversible protocol carriers.
Stream conversion reconciles terminal-only output and usage before closing the client stream.
A missing or unsuccessful terminal event causes stream failure.

Reasoning replay can restore missing reasoning next to matching tool calls in the same account, model, session, and agent.
The cache expires after fifteen minutes and has bounded entry and byte limits.
Replay requires a stable session identity and an exact tool-call match.
It does not guess a conversation from prompt text.

Enable `prompt_cache_key` to derive a scoped cache key from an identified session.
An explicit caller key takes priority.
A request without a session does not receive a generated key.

## Optional Compaction

Add exact Copilot model IDs to `compaction_models` to enable summary compaction.
The plugin supports Codex `compaction_trigger` requests and the host `responses/compact` operation.
It asks the Responses endpoint for a summary and appends one authenticated opaque compaction item.
Completed native compaction output passes through unchanged.
Capsules are encrypted and bound to the account credential, API origin, model, and protocol endpoint.
The plugin expands its own valid capsule into background history on a later request.
It rejects tampered capsules and capsules from a different scope.
Credential replacement invalidates capsules issued with the previous credential.

This compatibility summary does not recreate a provider's hidden reasoning state.
Enable it only for models where a plain text summary meets the client's history requirement.

## Limits and Evidence

The plugin can preserve only data that the client sends or that its scoped replay cache retains.
Codex can remove certain provider item IDs before sending a request.
An upstream model can reject unsupported parameters or protocol features.
A reversible tool-ID carrier can exceed another provider's identifier length limit.
Avoid a protocol conversion when the downstream provider cannot accept the resulting identifier.
Use a native endpoint for opaque history whenever the model supports it.

Package tests use synthetic data and mock transports.
Native host integration starts a disposable CLIProxyAPI process and a local mock upstream.
Real account login is a separate operator check and does not certify every live model or client.
See [CONTRIBUTING.md](CONTRIBUTING.md) for validation and [engineering contracts](docs/engineering-contracts.md) for implementation boundaries.
