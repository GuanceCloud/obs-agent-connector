# Command Reference

## Usage

```bash
obs-agent-connector <command> [arguments]
```

## Commands

| Command | Purpose |
| --- | --- |
| `list` | List installed Agent plugins detected on the local machine. |
| `discover` | Detect supported local Agents and install any missing plugins by using connector defaults from `config.json`. |
| `install <agent>` | Install one Agent plugin using the remote plugin installer. |
| `enable <agent>` | Enable one installed Agent plugin by setting its runtime JSON `enabled` switch to `true`. |
| `disable <agent>` | Disable one installed Agent plugin by setting its runtime JSON `enabled` switch to `false`. |
| `update <agent>` | Update one installed Agent plugin without modifying its current configuration. |
| `remove <agent>` | Remove one installed Agent plugin. Configuration files are kept unless `--purge-config` is used. |
| `version` | Show the current CLI version, check the latest GitHub release, and print or run a matching self-update action when a newer release is available. |

## Bootstrap

Initialize the CLI and save shared OBS defaults:

```bash
curl -fsSL -O https://static.guance.com/obs-agent-connector/install.sh
sh install.sh --endpoint=https://llm-openway.guance.com --x-token=agent_xxx
```

The installer writes:

- `download_base_url`
- `endpoint`
- `x_token`

into `~/.obs-agent-connector/config.json`.
When no download source is supplied, the installer derives it from the endpoint root domain and verifies the selected package against `SHA256SUMS`.

## Discover

Auto-install missing plugins for detected local Agents:

```bash
obs-agent-connector discover
```

Preview only:

```bash
obs-agent-connector discover --dry-run
```

Override stored defaults for a single run:

```bash
obs-agent-connector discover \
  --endpoint https://llm-openway.guance.com \
  --x-token agent_xxx \
  --yes
```

`discover` detects supported Agent commands in `PATH`, skips Agents whose plugins are already installed, generates one `agent_id` per new plugin, and uses `<hostname>_<agent>_<YYYYMMDD>` as the default `agent_name`.
Qoder is skipped until either `~/.qoder` or `~/.qoder-cn` has been created by the Agent.
Missing or invalid connector defaults are reported as `discover failed` errors.

## Install

Install one plugin with stored connector defaults:

```bash
obs-agent-connector install codex
```

Override stored defaults or identity values:

```bash
obs-agent-connector install codex \
  --endpoint https://llm-openway.guance.com \
  --x-token agent_xxx \
  --agent-id agent_xxx \
  --agent-name production-codex \
  --yes
```

By default, `install` reuses the CLI download source recorded in `~/.obs-agent-connector/config.json`.
If that source is unavailable, `install` derives the installer base from `--endpoint`.
For example, `https://llm-openway.guance.com` maps to `https://static.guance.com`, and `https://llm-openway.truewatch.com` maps to `https://static.truewatch.com`.
Use `--static-base` when you need to override the installer base.
On Windows, plugin installation does not use the OSS shell installer. The CLI downloads each supported plugin's GitHub release PowerShell installer instead.
Only `codex`, `openclaw`, and `qoder` are currently supported on Windows. Unsupported Agents return a friendly error.

When `--agent-id` or `--agent-name` are omitted, the CLI generates them automatically.

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
On Windows, `update` also uses the plugin's GitHub release PowerShell installer and follows the same support matrix as `install`.

For `qoder`, the CLI also detects the local layout and passes the matching `--variant cn` or `--variant global` flag before running the installer.

## Enable And Disable

Enable one installed plugin:

```bash
obs-agent-connector enable codex
```

Disable one installed plugin:

```bash
obs-agent-connector disable codex
```

Preview the config change without writing:

```bash
obs-agent-connector disable codex --dry-run
```

`enable` and `disable` update the Agent runtime JSON config in place:

- `claude`, `codex`, and `qoder` set top-level `enabled`
- `openclaw` sets `plugins.entries.openclaw-otel-plugin.enabled`

`hermes` is not currently supported because its runtime config is YAML rather than a supported JSON `enabled` switch.

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

## Version

Show the current version and check for a newer release:

```bash
obs-agent-connector version
```

Run self-update directly when a newer release is available:

```bash
obs-agent-connector version -u
```

`version` reads CLI metadata from `~/.obs-agent-connector/config.json`. The standard installer writes `download_base_url`, `endpoint`, and `x_token`, and later self-update commands use the same download source.

Skip the remote release check:

```bash
obs-agent-connector version --offline
```
