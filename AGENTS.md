# AGENTS.md

## Project

This repository contains `obs-agent-connector`, a Go-based CLI for installing and managing OBS/GTrace Agent plugins.

The CLI supports plugin lifecycle operations for:

- `claude`
- `codex`
- `hermes`
- `openclaw`
- `qoder`
- `qoder-cn`

## Language

All user-facing CLI text and all project documentation must be written in English.

Before finishing documentation or CLI text changes, check for non-English Chinese text:

```bash
rg -n "[\p{Han}]" README.md docs scripts cmd internal AGENTS.md .gitignore go.mod
```

## Development

Prefer small, focused changes. Keep each Agent definition and its variant-specific behavior in the matching file under `internal/agent`. Keep command handlers and shared application workflows under `internal/app`. The executable entry point belongs in `cmd/obs-agent-connector`.

Do not add Agent-specific branches to `internal/agent/registry.go`. Use the resolver hooks on `agent.Definition` when an Agent needs custom installation or discovery behavior.

Do not generate Agent runtime configuration directly in this CLI. Runtime configuration files such as `gtrace.json`, `config.yaml`, or `openclaw.json` are owned by each Agent plugin installer.

## Commands

Run these checks before handing off changes:

```bash
gofmt -w $(find cmd internal -type f -name '*.go')
go test ./...
go vet ./...
go build -o obs-agent-connector ./cmd/obs-agent-connector
```

For release packaging:

```bash
./scripts/build-release.sh
```

Release artifacts are written to `dist/`.

## CLI Behavior

Keep the public command model simple:

```bash
obs-agent-connector list
obs-agent-connector discover
obs-agent-connector install <agent>
obs-agent-connector update <agent>
obs-agent-connector remove <agent>
obs-agent-connector version
```

Rules:

- `update` must require one explicit Agent name and must not support `all`.
- `update cli` is intentionally unsupported. Future CLI version management should be implemented under a separate `version` command.
- `install` resolves `Endpoint` and `X-Token` from flags or connector config, and generates `Agent ID` and `Agent Name` unless explicitly provided.
- `install` always passes `--type gtrace` to plugin installers.
- `update` must preserve existing configuration by passing `--no-config`.
- `remove` must keep configuration files by default; only delete config files when `--purge-config` is provided.

## Qoder Variants

`qoder` and `qoder-cn` are separate Agent targets.

- `qoder` is the international edition and uses `QODER_HOME=~/.qoder`.
- `qoder-cn` is the China edition and uses `QODER_HOME=~/.qoder-cn`.

Both use the `qoder-otel-plugin` installer.

## Documentation

Keep `README.md` concise. It should describe the project, main features, supported Agents, common commands, build steps, and documentation links.

Detailed information belongs in:

- `docs/commands.md`
- `docs/plugins.md`
- `docs/distribution.md`

## Release Artifacts

The release script should produce:

- `obs-agent-connector-darwin-arm64.tar.gz`
- `obs-agent-connector-darwin-amd64.tar.gz`
- `obs-agent-connector-linux-amd64.tar.gz`
- `obs-agent-connector-linux-arm64.tar.gz`
- `obs-agent-connector-windows-amd64.zip`
- `obs-agent-connector-windows-arm64.zip`
- `SHA256SUMS`
