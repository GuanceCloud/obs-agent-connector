# Command Reference

## Usage

```bash
obs-agent-connector <command> [arguments]
```

## Commands

| Command | Purpose |
| --- | --- |
| `list` | List installed Agent plugins detected on the local machine. |
| `doctor [agent]` | Diagnose missing commands, missing plugin files, missing config files, and optional remote installer reachability. If no agent is provided, all supported agents are checked. |
| `install <agent>` | Install one Agent plugin using the remote plugin installer. |
| `update <agent>` | Update one installed Agent plugin without modifying its current configuration. |
| `remove <agent>` | Remove one installed Agent plugin. Configuration files are kept unless `--purge-config` is used. |
| `version` | Show the current CLI version, check the latest GitHub release, and print a matching self-update command when a newer release is available. |

## Install

Interactive install:

```bash
obs-agent-connector install codex
```

Non-interactive install:

```bash
obs-agent-connector install codex \
  --endpoint https://llm-openway.guance.com \
  --x-token agent_xxx \
  --agent-id agent_xxx \
  --agent-name production-codex \
  --yes
```

By default, `install` derives the installer base from `--endpoint`.
For example, `https://llm-openway.guance.com` maps to `https://static.guance.com`, and `https://llm-openway.truewatch.com` maps to `https://static.truewatch.com`.
Use `--static-base` when you need to override the derived installer base.

Preview only:

```bash
obs-agent-connector install codex \
  --endpoint https://llm-openway.guance.com \
  --x-token agent_xxx \
  --agent-id agent_xxx \
  --agent-name production-codex \
  --dry-run
```

## Update

Update one installed plugin:

```bash
obs-agent-connector update codex
```

`update` intentionally requires a single Agent name.

Plugin updates preserve existing configuration by passing `--no-config` to the plugin installer.

For `qoder`, the CLI also detects the local layout and passes the matching `--variant cn` or `--variant global` flag before running the installer.

## Remove

Remove a plugin and keep configuration files:

```bash
obs-agent-connector remove codex
```

Remove a plugin and its configuration files:

```bash
obs-agent-connector remove codex --purge-config
```

Preview removal:

```bash
obs-agent-connector remove codex --dry-run
```

## Doctor

Show only issues:

```bash
obs-agent-connector doctor
```

Show all checks:

```bash
obs-agent-connector doctor --verbose
```

Check remote installer scripts:

```bash
obs-agent-connector doctor --online
```

## Version

Show the current version and check for a newer release:

```bash
obs-agent-connector version
```

`version` reads release metadata from `OBS_AGENT_CONNECTOR_CONFIG` when that variable is set by the installer wrapper. The standard installer writes brand-specific config under `~/.obs-agent-connector/<brand>/config.json`.

Skip the remote release check:

```bash
obs-agent-connector version --offline
```
