# obs-agent-connector

`obs-agent-connector` is a command-line tool for installing and managing OBS/GTrace Agent plugins across multiple AI coding agents.

The tool provides a single entry point for plugin installation, update, removal, local status inspection, and environment diagnostics. It delegates the final runtime configuration generation to each Agent plugin installer.

## Features

- Install Agent plugins through the official remote installer scripts.
- Collect required OBS/GTrace parameters interactively.
- Update one installed Agent plugin without modifying existing configuration.
- Remove installed plugins while keeping configuration by default.
- Detect installed plugins and their configuration paths.
- Diagnose local environment issues with `doctor`.
- Show the current CLI version and check whether a newer GitHub release is available.
- Support separate Qoder international and China editions.
- Build release packages for macOS, Linux, and Windows.

## Supported Agents

| Agent | Notes |
| --- | --- |
| `claude` | Claude plugin |
| `codex` | Codex plugin |
| `hermes` | Hermes plugin |
| `openclaw` | OpenClaw plugin |
| `qoder` | Qoder plugin. The CLI auto-detects CN vs global layout and passes the matching `--variant` value. |

## Common Commands

```bash
obs-agent-connector list
obs-agent-connector doctor
obs-agent-connector install codex
obs-agent-connector install qoder
obs-agent-connector update codex
obs-agent-connector remove codex
obs-agent-connector version
```

For Qoder installs, `obs-agent-connector` detects the local layout and uses:

- `--variant cn` with `~/.qoder-cn` when the CN layout is detected
- `--variant global` with `~/.qoder` when the global layout is detected

For plugin installation, `obs-agent-connector` also derives the default installer base from `--endpoint`.
For example, `https://llm-openway.guance.com` maps to `https://static.guance.com`, and `https://llm-openway.truewatch.com` maps to `https://static.truewatch.com`.
Use `--static-base` to override this behavior.

Compatibility note:

- `qoder-cn` is still accepted as a legacy compatibility target and always forces the CN layout.

During installation, the CLI prompts for:

```text
Endpoint
X-Token
Agent ID
Agent Name
```

## Build

Build a local binary:

```bash
go build -o obs-agent-connector .
```

Build release packages:

```bash
./scripts/build-release.sh
```

Release artifacts are written to `dist/`.
Tagged release builds embed the Git tag as the CLI version.

GitHub Actions:

- `CI` runs on pushes and pull requests.
- `Package` runs manually and uploads packaged artifacts as a workflow artifact.
- `Release` runs on tags matching `v*`, reuses the `Package` workflow, renders release notes from the repository template, and publishes the same artifacts to GitHub Releases.

## Project Layout

```text
.
├── docs/                 Detailed documentation
├── .github/workflows/    CI and release workflows
├── scripts/              Build and release scripts
├── dist/                 Generated release artifacts
├── main.go               CLI implementation
├── go.mod
└── README.md
```

## Documentation

- [Command reference](docs/commands.md)
- [Plugin matrix](docs/plugins.md)
- [Distribution guide](docs/distribution.md)
