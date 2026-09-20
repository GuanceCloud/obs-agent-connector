# obs-agent-connector

`obs-agent-connector` is a single Go binary for installing and managing OBS/GTrace integrations and collecting telemetry from AI coding agents.

The current stable release is [v0.1.24](https://github.com/GuanceCloud/obs-agent-connector/releases/tag/v0.1.24). See the [release notes](docs/releases/v0.1.24.md) for changes and compatibility details.

## Features

- Built-in telemetry adapters for Claude, CodeBuddy, Codex, Cursor, Deep Agents Code, Grok Build, Kiro CLI, OMP, and WorkBuddy, with one binary and one version.
- External plugin installation through standard OSS or GitHub Release installers.
- Local Agent discovery, installation, updates, status, configuration, enable/disable, and removal.
- Shared endpoint and X-Token defaults, automatically generated Agent identities, and configuration-preserving plugin updates.
- CLI version checks and self-update with `version -u`.
- Verified release packages and native installers for macOS, Linux, and Windows on AMD64 and ARM64.

## Install

macOS / Linux:

```bash
curl -fsSL -O https://static.guance.com/obs-agent-connector/install.sh && \
sh install.sh --endpoint=https://llm-openway.guance.com --x-token=agent_xxx
```

Windows PowerShell:

```powershell
Invoke-WebRequest -Uri "https://static.guance.com/obs-agent-connector/install.ps1" -OutFile "install.ps1"
.\install.ps1 -Endpoint "https://llm-openway.guance.com" -XToken "agent_xxx"
```

The installer verifies `SHA256SUMS`, installs the binary, and records the download source and shared defaults in `~/.obs-agent-connector/config.json`. Reload your shell if the installer updates `PATH`. Run command-line binaries from a terminal; do not double-click them in macOS Finder.

For a specific version or GitHub download source, see the [distribution guide](docs/distribution.md).

## Supported Agents

| Agent | Plugin | macOS | Linux | Windows | Notes |
| --- | --- | --- | --- | --- | --- |
| `claude` | Built into `obs-agent-connector` | `✅` | `✅` | `✅` | Stop / SessionEnd Hook adapter |
| `codebuddy` | Built into `obs-agent-connector` | `✅` | `✅` | `✅` | Stop / SessionEnd Hook plus native `index.json` replay; Linux x64 is product-validated |
| `codex` | Built into `obs-agent-connector` | `✅` | `✅` | `✅` | Stop Hook adapter plus built-in Codex trust/config handling |
| `cursor` | Built into `obs-agent-connector` | `✅` | `✅` | `✅` | Detects `~/.cursor`, prefers `cursor-agent`, and manages user-level Cursor Hooks |
| `dcode` | Built into `obs-agent-connector` | `✅` | `✅` | `✅` | Hooks v2 plus transcript replay; `Stop` completes normal turns and failed sessions fall back to `SessionEnd(reason=other)` |
| `grok` | Built into `obs-agent-connector` | `✅` | `✅` | `✅` | Grok Build CLI 1.0.5+; TUI/headless Hooks plus terminal `updates.jsonl` replay |
| `kiro` | Built into `obs-agent-connector` | `✅` | `✅` | `✅` | V3 interactive TTY only (`kiro-cli chat --v3`); default V2 and `--no-interactive` do not load global Hooks |
| `omp` | Built into `obs-agent-connector` | `✅` | `✅` | `✅` | OMP 18.1.15+; macOS ARM64 product-tested; Linux/Windows build-only validation |
| `dsh` | `dsh-otel-plugin` | `✅` | `✅` | `✅` | DeepSeek Harness profile bundle |
| `hermes` | `hermes-otel-plugin` | `✅` | `✅` | `❌` | Hermes plugin |
| `opencode` | `opencode-otel-plugin` | `✅` | `✅` | `✅` | Uses the OpenCode config directory under `~/.config/opencode` |
| `mimo` | `opencode-otel-plugin` (MiMo variant) | `✅` | `✅` | `✅` | Native MiMo paths; requires the host-aware plugin release |
| `openclaw` | `openclaw-otel-plugin` | `✅` | `✅` | `✅` | OpenClaw plugin |
| `qoder` | `qoder-otel-plugin` | `✅` | `✅` | `✅` | Auto-detects CN vs global layout and passes the matching `--variant` value |
| `workbuddy` | Built into `obs-agent-connector` | `✅` | `❌` | `✅` | Multi-Hook journal plus JSONL replay; restart after migrating from the external plugin |

`qoder` automatically selects the global (`~/.qoder`) or CN (`~/.qoder-cn`) layout. The legacy `qoder-cn` target remains available and forces the CN layout.

Platform support describes connector installation and packaging; product runtime requirements and validation limits are listed above and in the [plugin matrix](docs/plugins.md).

## Common Commands

```bash
obs-agent-connector agents                 # List supported Agents and platforms
obs-agent-connector discover               # Detect Agents and install missing plugins
obs-agent-connector discover -u            # Sync all detected plugins
obs-agent-connector install codex          # Install one Agent integration
obs-agent-connector install omp
obs-agent-connector install workbuddy
obs-agent-connector list                   # List installed plugins
obs-agent-connector status codex
obs-agent-connector config codex list
obs-agent-connector config codex edit --enabled=false
obs-agent-connector enable codex
obs-agent-connector disable codex
obs-agent-connector update codex           # Preserve existing plugin configuration
obs-agent-connector remove codex
obs-agent-connector uninstall
obs-agent-connector version
obs-agent-connector version -u             # Update the connector binary
```

`install` and `discover` reuse stored endpoint and X-Token defaults and generate Agent ID and Agent Name unless explicitly supplied. `update` requires one Agent name and preserves configuration with `--no-config`.

Built-in adapters store configuration, Hook logs, and replay state under `~/.obs-agent-connector/<agent>/`. External installers own their runtime configuration. Removing a built-in adapter deletes its managed Hooks and directory; legacy Agent-local configuration remains unless `--purge-config` is supplied. Use `uninstall --keep-config` to retain connector-managed configuration.

After migrating WorkBuddy from the external plugin, restart WorkBuddy to load the managed Hooks. See [WorkBuddy migration](docs/product-research/workbuddy.md). OMP uses a bundled native extension and the built-in Go collector; see [OMP setup and limitations](docs/product-research/omp.md).

## Build

Requires Go 1.22 or later.

```bash
go test ./...
go vet ./...
go build -o obs-agent-connector ./cmd/obs-agent-connector
VERSION=v0.1.24 ./scripts/build-release.sh
```

Release artifacts are written to `dist/`: six platform archives, installer scripts, `latest.txt`, and `SHA256SUMS`. Tagged builds embed the tag as the CLI and built-in adapter version.

GitHub Actions runs CI on pushes and pull requests. The manual `Package` workflow creates downloadable artifacts. Tags matching `v*` trigger `Release`, which reuses the packaging workflow and publishes to GitHub Releases; RC tags are prereleases.

## Project Layout

- `cmd/obs-agent-connector/`: executable entry point.
- `internal/app/`: commands and shared application workflows.
- `internal/agent/`: Agent definitions and discovery.
- `internal/adapters/`: product-specific telemetry collectors.
- `internal/core/`: shared Trace, Metrics, OTLP, privacy, and state logic.
- `internal/install/`: built-in Hook and runtime-config installers.
- `scripts/`: installation, build, and release scripts.
- `docs/`: usage, commands, integration details, and release notes.

## Documentation

- [Usage guide](docs/usage.md)
- [Command reference](docs/commands.md)
- [Plugin matrix](docs/plugins.md)
- [Distribution guide](docs/distribution.md)
- [DCode checkpoint collection](docs/dcode-checkpoint-collection.md)
- [Release notes](docs/releases/v0.1.24.md)

## License

This project is licensed under the [Apache License 2.0](LICENSE).
