# obs-agent-connector

`obs-agent-connector` is the single command-line runtime for installing, managing, and collecting OBS/GTrace telemetry across multiple AI coding agents.

The tool provides one binary and one version for connector lifecycle operations and built-in telemetry adapters for Claude, CodeBuddy, and Codex. Other Agents continue to use their external plugins.

## Features

- Bootstrap the CLI and OBS defaults with one installer command.
- Collect Claude, CodeBuddy, and Codex terminal turns through built-in adapters without separate repositories.
- Install transitional external Agent plugins through their official remote installer scripts.
- Auto-discover local Agents, install missing plugins, and sync all plugins with `discover -u`.
- Reuse stored `endpoint` and `x-token` defaults from `~/.obs-agent-connector/config.json`.
- Update one installed Agent plugin without modifying existing configuration.
- Enable or disable an installed plugin by updating its runtime config.
- Remove installed plugins while keeping configuration by default.
- Detect installed plugins and their configuration paths.
- Show the current CLI version and check whether a newer GitHub release is available.
- Run CLI self-update directly from the `version -u` command.
- Support separate Qoder international and China editions.
- Install the CLI through a dedicated installer script.
- Use native installers for Unix shell and Windows PowerShell.
- Keep CLI download metadata in `~/.obs-agent-connector/config.json`.
- Build release packages for macOS, Linux, and Windows.

## Supported Agents

| Agent | Plugin | macOS | Linux | Windows | Notes |
| --- | --- | --- | --- | --- | --- |
| `claude` | Built into `obs-agent-connector` | `✅` | `✅` | `✅` | Stop / SessionEnd Hook adapter |
| `codebuddy` | Built into `obs-agent-connector` | `✅` | `✅` | `✅` | Stop / SessionEnd Hook plus native `index.json` replay; Linux x64 is product-validated |
| `codex` | Built into `obs-agent-connector` | `✅` | `✅` | `✅` | Stop Hook adapter plus built-in Codex trust/config handling |
| `cursor` | `cursor-otel-plugin` | `✅` | `✅` | `✅` | Detects `~/.cursor` or the Cursor CLI family and manages a user-level Cursor Hook plugin |
| `hermes` | `hermes-otel-plugin` | `✅` | `✅` | `❌` | Hermes plugin |
| `opencode` | `opencode-otel-plugin` | `✅` | `✅` | `✅` | Uses the OpenCode config directory under `~/.config/opencode` |
| `openclaw` | `openclaw-otel-plugin` | `✅` | `✅` | `✅` | OpenClaw plugin |
| `qoder` | `qoder-otel-plugin` | `✅` | `✅` | `✅` | Auto-detects CN vs global layout and passes the matching `--variant` value |
| `workbuddy` | `workbuddy-otel-plugin` | `✅` | `❌` | `✅` | Uses the detected WorkBuddy profile directory and writes `gtrace.json` there |

## Common Commands

```bash
obs-agent-connector list
obs-agent-connector status codex
obs-agent-connector discover
obs-agent-connector discover -u
obs-agent-connector install codex
obs-agent-connector install codebuddy
obs-agent-connector install cursor
obs-agent-connector config codex list
obs-agent-connector config codex edit --enabled=false --endpoint=https://llm-openway.truewatch.com
obs-agent-connector install opencode
obs-agent-connector install qoder
obs-agent-connector install workbuddy
obs-agent-connector enable codex
obs-agent-connector disable codex
obs-agent-connector update codex
obs-agent-connector remove codex
obs-agent-connector uninstall
obs-agent-connector version
obs-agent-connector version -u
```

For Qoder installs, `obs-agent-connector` detects the local layout and uses:

- `--variant cn` with `~/.qoder-cn` when the CN layout is detected
- `--variant global` with `~/.qoder` when the global layout is detected

For plugin installation, `obs-agent-connector` first reuses the CLI download source recorded in `~/.obs-agent-connector/config.json`.
If that source is unavailable, the CLI derives the installer base from `--endpoint` by mapping the root domain to `https://static.<root-domain>`.
Use `--static-base` to override this behavior.

Compatibility note:

- `qoder-cn` is still accepted as a legacy compatibility target and always forces the CN layout.
- On Windows, `claude`, `codebuddy`, `codex`, `cursor`, `opencode`, `openclaw`, `qoder`, and `workbuddy` are supported.
- Claude, CodeBuddy, and Codex register the connector directly; external plugins use their GitHub release PowerShell installer instead of the OSS shell installer.

Bootstrap the CLI with shared defaults:

```bash
curl -fsSL -O https://static.guance.com/obs-agent-connector/install.sh && \
sh install.sh --endpoint=https://llm-openway.guance.com --x-token=agent_xxx
```

On first install, the script derives `download_base_url` from the endpoint root domain.
For example, `https://llm-openway.guance.com` maps to `https://static.guance.com/obs-agent-connector`.
The downloaded package is verified against `SHA256SUMS` before installation.

After bootstrap, use `discover` to auto-install missing plugins, or use `install <agent>` for a single Agent.
Claude, CodeBuddy, and Codex are managed as built-in adapters with ordinary commands such as `install <agent>`, `status <agent>`, and `remove <agent>`.
`install` and `discover` generate `agent_id` and `agent_name` automatically when you do not pass them explicitly.
The default `agent_id` uses the format `agid_<uuidv4-without-dashes>`.
The default name uses `<hostname>_<agent>_<YYYYMMDD>`, for example `liurui_claude_20260715`.
`list` and `discover` also show the detected plugin version when it can be resolved from the local install layout.
`status <agent>` prints a single-Agent view including install state, version, config path, plugin path, and runtime `enabled` status when the plugin uses a supported JSON config.
`config <agent> list` shows the current managed `gtrace.json` parameters for supported Agents. `config <agent> edit` merges one or more parameters into the existing file and rewrites it.
Qoder is considered installed only when `~/.qoder` or `~/.qoder-cn` exists.
OpenCode is discovered when the `opencode` command is in `PATH` or when `~/.config/opencode` already exists.
Cursor is discovered when the `cursor` or `cursor-agent` CLI is in `PATH`, or when `~/.cursor` already exists.
WorkBuddy is considered installed only when its profile directory already exists, for example `~/.workbuddy`.
`enable <agent>` and `disable <agent>` update the plugin runtime `enabled` switch in its JSON config file. `hermes` is excluded because its runtime config is YAML.
`config` currently supports the managed `gtrace.json` layout used by `claude`, `codebuddy`, `codex`, `cursor`, `opencode`, `qoder`, and `workbuddy`. `hermes` and `openclaw` are excluded.
`remove codebuddy`, `remove claude`, and `remove codex` remove only connector-managed Hooks by default and preserve runtime config unless `--purge-config` is supplied. `uninstall` removes the connector binary and its managed built-in Hooks while preserving Agent config and state.

## Build

Build a local binary:

```bash
go build -o obs-agent-connector ./cmd/obs-agent-connector
```

Build release packages:

```bash
./scripts/build-release.sh
```

Release artifacts are written to `dist/`.
Tagged release builds embed the Git tag as the CLI version.

On macOS, do not double-click the extracted binary in Finder.
Finder launches command-line executables through Terminal and appends `; exit;` automatically. Run the binary from Terminal instead.

Preferred CLI installation uses the release installer script.
The installer writes `~/.obs-agent-connector/config.json`, including `download_base_url`, `endpoint`, and `x_token`.
`install`, `discover`, `version`, and self-update reuse that file.
Use `install.sh` on macOS/Linux and `install.ps1` on Windows.

GitHub Actions:

- `CI` runs on pushes and pull requests.
- `Package` runs manually and uploads packaged artifacts as a workflow artifact.
- `Release` runs on tags matching `v*`, reuses the `Package` workflow, renders release notes from commit subjects, and publishes the same artifacts to GitHub Releases.

## Project Layout

```text
.
├── docs/                 Detailed documentation
├── .github/workflows/    CI and release workflows
├── scripts/              Build and release scripts
├── dist/                 Generated release artifacts
├── cmd/
│   └── obs-agent-connector/  Executable entry point
├── internal/
│   ├── adapters/         Built-in Agent telemetry adapters
│   ├── agent/            Agent definitions, discovery, and registry
│   ├── app/              Commands, installation, config, and version flows
│   ├── core/             Turn, Trace, Metrics, OTLP, privacy, and state logic
│   └── install/          Built-in Hook and runtime-config installers
├── go.mod
└── README.md
```

## Documentation

- [Usage guide](docs/usage.md)
- [Command reference](docs/commands.md)
- [Plugin matrix](docs/plugins.md)
- [Distribution guide](docs/distribution.md)

## License

This project is licensed under the [Apache License 2.0](LICENSE).
