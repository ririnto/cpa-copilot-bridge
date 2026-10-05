# Contributing

## Commands

The Go module pins Task v3.54.0 through a `tool` directive.
Use the module-managed executable instead of a global Task installation.

```bash
go tool task fmt
go tool task test
go tool task test-race
go tool task vet
go tool task build
go tool task check
```

`check` runs formatting checks, race tests, static checks, and the native build.
Build output uses `-trimpath` and excludes embedded Git metadata.
CGO requires a C toolchain for each target platform.
The build task targets the current platform.

## Host Integration

Build a CLIProxyAPI v8 host from its normal `cmd/server` entry point.
Supply its executable through `CPA_BINARY`.

```bash
CPA_BINARY=/path/to/cli-proxy-api go tool task integration
```

The test copies the native plugin into a temporary host installation.
All credentials and upstream responses are synthetic.
The integration package skips its host test when `CPA_BINARY` is absent.
A skipped host test does not count as native loading evidence.

Run focused tests for affected behavior before the repository gate.
Reuse passing evidence while source, dependencies, configuration, and toolchain inputs remain unchanged.
Report the exact commands and distinguish mock evidence from live provider checks.

## Delivery

Develop on a named working branch and publish a pull request targeting `main`.
Use one independent review of the published changes.
Fix confirmed correctness, security, or acceptance blockers before merging.
Record noncritical follow-ups as focused issues with an owner and acceptance criteria.
Use branch names and source tags for durable references.
Preserve the configured Git author and committer identity.

## Private Data

Keep real auth files, local configuration, logs, and captured payloads outside tracked paths.
Use ignored local runtime storage only for operator checks.
Keep authentication storage outside the checkout.
Review staged content and artifact strings for local paths or credentials before publishing.
Use portable paths and synthetic values in documentation and issue bodies.
