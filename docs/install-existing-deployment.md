# Install into an existing CLIProxyAPI deployment

This guide adds the GitHub Copilot plugin to an existing official CLIProxyAPI deployment.
It keeps the existing configuration, API keys, and providers.
The plugin targets CLIProxyAPI `v8.0.15`, ABI version 1, on Linux `amd64`.

## 1. Build the plugin

```bash
git clone https://github.com/arthur-sommer-etc/cliproxyapi-copilot-plugin.git
cd cliproxyapi-copilot-plugin
make build
```

The resulting library is:

```text
build/plugins/linux/amd64/cliproxyapi-copilot.so
```

The filename is significant: CLIProxyAPI derives the plugin ID
`cliproxyapi-copilot` from it. Do not rename the library unless the matching key
under `plugins.configs` is also renamed.

## 2. Install the library

CLIProxyAPI searches both `<plugins.dir>/linux/amd64` and `<plugins.dir>`.
Using the platform-specific directory avoids loading an incompatible binary.

### Native CLIProxyAPI

Choose a permanent plugin directory and copy the library:

```bash
sudo install -d -m 0755 /opt/cliproxyapi/plugins/linux/amd64
sudo install -m 0755 \
  build/plugins/linux/amd64/cliproxyapi-copilot.so \
  /opt/cliproxyapi/plugins/linux/amd64/cliproxyapi-copilot.so
```

The CLIProxyAPI process must be able to read the library. Use a different
absolute directory if `/opt/cliproxyapi` does not match the deployment.

### Docker or Docker Compose

Copy the library into a persistent host directory:

```bash
install -d -m 0755 /path/to/cliproxyapi/plugins/linux/amd64
install -m 0755 \
  build/plugins/linux/amd64/cliproxyapi-copilot.so \
  /path/to/cliproxyapi/plugins/linux/amd64/cliproxyapi-copilot.so
```

Mount that directory into the existing container:

```yaml
services:
  cliproxyapi:
    volumes:
      - /path/to/cliproxyapi/plugins:/CLIProxyAPI/plugins:ro
```

Merge the mount into the existing service rather than replacing its current
configuration and auth-volume mounts.

## 3. Merge the plugin configuration

This example uses the CLIProxyAPI v8 configuration layout.
Add or merge this block under the top-level `plugins` key in the existing `config.yaml`:

```yaml
plugins:
  enabled: true
  dir: "/opt/cliproxyapi/plugins" # Native deployment
  configs:
    cliproxyapi-copilot:
      enabled: true
      priority: 100
      github_client_id: "Iv1.b507a08c87ecfe98"
      github_scope: "read:user"
      github_base_url: "https://github.com"
      github_api_url: "https://api.github.com"
      copilot_api_url: "https://api.githubcopilot.com"
      oauth_timeout_seconds: 900
      model_cache_ttl_seconds: 600
      token_expiry_buffer_seconds: 300
      support-prompt-cache-key: true
```

For the Docker mount above, use:

```yaml
plugins:
  enabled: true
  dir: "/CLIProxyAPI/plugins"
```

There must be only one top-level `plugins` key. Preserve other entries already
present under `plugins.configs`. Global `plugins.enabled` and the individual
`cliproxyapi-copilot.enabled` setting must both be `true`.

The `oauth.auth-dir` directory must be writable and persistent.
CLIProxyAPI stores the plugin's GitHub OAuth credential there.

Review [GitHub's supported Copilot models](https://github.com/github/docs/blob/main/content/copilot/reference/ai-models/supported-models.md) when updating this filter.
The v8 OAuth filter excludes selected older Copilot model IDs by family and exact name.
It affects Copilot only.
Matching IDs from other provider catalogs remain available.
Copilot model registration shows only upstream models available to the authenticated account.

```yaml
excluded-models:
  copilot:
    - "gpt-5*"
    - "gpt-6-sol"
    - "claude-fable-5"
    - "claude-sonnet-4*"
    - "claude-sonnet-5"
    - "claude-opus-4*"
    - "claude-opus-5"
    - "gemini-3.7-flash"
    - "grok-4.5"
    - "grok-4.6"
```

The exact Claude exclusions do not filter `claude-opus-5.5` or `claude-sonnet-5.5`.

Add this native v8 `model-alias` block to the existing top-level `oauth` section.

```yaml
model-alias:
  copilot:
    - name: "claude-opus-5.5"
      alias: "claude-opus-5-5"
      fork: true
    - name: "claude-sonnet-5.5"
      alias: "claude-sonnet-5-5"
      fork: true
    - name: "claude-fable-5.1"
      alias: "claude-fable-5-1"
      fork: true
```

When Copilot exposes an upstream model, the aliases keep its dotted ID and add these client names.
CLIProxyAPI sends the original dotted model IDs to Copilot for aliased requests.
The configuration does not force response model IDs to change.

Add this native v8 `settings` block to the existing top-level `oauth` section.

```yaml
settings:
  copilot:
    - name: "gpt-6.1-sol"
      max-context-length: 272000
    - name: "gpt-6-luna"
      max-context-length: 272000
```

This global setting changes only the advertised context metadata for Copilot OAuth accounts.
It does not enforce a billing or usage-cost cap.
Thinking support and other capabilities remain based on Copilot model metadata.
The native host setting needs no plugin-specific option or auth-file field.

Chat Completions, Responses, and Messages requests can route to Copilot Chat Completions, Responses, or Messages endpoints.
The plugin selects an endpoint from model metadata.
Set `model_endpoint_overrides` when a model needs a fixed route.
Send full caller history to Copilot Chat or Messages because the plugin does not store conversations.
Use `previous_response_id` only with a native Copilot Responses endpoint that supports it.
The plugin stores an authenticated replay carrier with the Copilot credential.
It accepts the carrier only for the same account, model, and endpoint.
The plugin rejects foreign signed or encrypted reasoning on Copilot Chat with HTTP 422.
Send full caller history for cross-format requests because the bridge cannot reconstruct it from `previous_response_id` alone.
The plugin derives `prompt_cache_key` for Copilot Responses requests when a stable session identity exists.
Set `support-prompt-cache-key: false` to omit generated keys.
The plugin preserves explicit caller keys.
Copilot's implicit cache lifetime can differ from Codex's.
The plugin enables compaction for listed models on compatible CLIProxyAPI hosts.
This option does not add unavailable models to `/v1/models`.

```yaml
compaction_models:
  - "gpt-6-luna"
  - "gpt-6.1-sol"
  - "mai-code-1.1-flash"
```

Codex also needs remote compaction enabled for its configured model provider.

```toml
[model_providers.cliproxyapi.capabilities]
remote_compaction = "v2"
```

Replace `cliproxyapi` with the provider name in your Codex configuration.

## 4. Restart and authenticate

Restart CLIProxyAPI using the deployment's normal service command. Examples:

```bash
sudo systemctl restart cliproxyapi
```

```bash
docker compose up -d --force-recreate cliproxyapi
```

The startup log should contain entries similar to:

```text
plugin loaded plugin_id=cliproxyapi-copilot
plugin registered plugin_id=cliproxyapi-copilot
```

Open the existing CLIProxyAPI management center, start the **Copilot** login,
and complete GitHub's device-code flow. The login uses the deployment's normal
management authentication and auth directory.

## 5. Verify the installation

Check plugin registration with the existing management password:

```bash
curl -fsS \
  -H "Authorization: Bearer $MANAGEMENT_PASSWORD" \
  http://127.0.0.1:8317/v0/management/plugins
```

After Copilot authentication, query the normal model endpoint with an existing
CLIProxyAPI client key:

```bash
curl -fsS \
  -H "Authorization: Bearer $API_KEY" \
  http://127.0.0.1:8317/v1/models
```

The result reflects the models available to the authenticated Copilot account.
Existing Claude and other provider models remain available.

## Updating or removing the plugin

Version tags automatically publish marketplace-compatible packages. To update
from a release, verify the archive against `checksums.txt`, extract
`cliproxyapi-copilot.so`, replace the installed library, and restart
CLIProxyAPI.

To disable it without deleting credentials:

```yaml
plugins:
  configs:
    cliproxyapi-copilot:
      enabled: false
```

To remove it completely, stop CLIProxyAPI, delete the installed library, remove
only the `cliproxyapi-copilot` configuration entry, and restart. Delete the
plugin's Copilot auth entry through the normal management UI only if the stored
credential should also be revoked or removed.
