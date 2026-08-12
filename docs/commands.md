# Command Reference

## Usage

```bash
obs-agent-connector <command> [arguments]
```

## Commands

| Command | Purpose |
| --- | --- |
| `list` | List installed Agent plugins detected on the local machine, including best-effort plugin version detection. |
| `status <agent>` | Show one Agent plugin status, including install state, config path, plugin path, version, and runtime `enabled` state when supported. |
| `discover` | Detect supported local Agents and install any missing plugins by using connector defaults from `config.json`. Use `discover -u` to update installed plugins and install any missing plugins in one run. |
| `install <agent>` | Install one Agent integration. CodeBuddy is built in; other Agents use their external plugins. |
| `config <agent>` | List or edit the managed runtime `gtrace.json` for supported Agents. |
| `enable <agent>` | Enable one installed Agent plugin by setting its runtime JSON `enabled` switch to `true`. |
| `disable <agent>` | Disable one installed Agent plugin by setting its runtime JSON `enabled` switch to `false`. |
| `update <agent>` | Update one installed Agent plugin without modifying its current configuration. |
| `remove <agent>` | Remove one installed Agent plugin. Configuration files are kept unless `--purge-config` is used. |
| `uninstall` | Uninstall `obs-agent-connector` itself. By default this removes the binary, connector config, and managed PATH entry when present. |
| `version` | Show the current CLI version, check the latest GitHub release, and print or run a matching self-update action when a newer release is available. |

CodeBuddy is built into the connector. Claude, Codex, and other Agents use their external plugins.

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

Update installed plugins and install any missing plugins in one run:

```bash
obs-agent-connector discover -u
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

`discover` detects supported Agent commands in `PATH`, skips Agents whose plugins are already installed by default, and switches to update mode when `-u` is provided.
For new plugin installs it generates one `agid_<uuidv4-without-dashes>` `agent_id` and uses `<hostname>_<agent>_<YYYYMMDD>` as the default `agent_name`.
The output also shows the detected plugin version when it can be resolved from the local install layout.
Qoder is skipped until either `~/.qoder` or `~/.qoder-cn` has been created by the Agent.
OpenCode is also detected when `~/.config/opencode` already exists, even if `opencode` is not currently in `PATH`.
CodeBuddy is detected when the `codebuddy` command is in `PATH` or `~/.codebuddy` exists.
Missing or invalid connector defaults are reported as `discover failed` errors.

## Status

Show one Agent plugin status:

```bash
obs-agent-connector status codex
```

The output includes:

- Agent name
- resolved command name
- whether the Agent is supported on the current platform
- whether the plugin is installed
- detected plugin version, when available
- runtime config path
- installed plugin path
- runtime `enabled` state for Agents that use a supported JSON config

For plugins such as `hermes`, whose runtime config is YAML, the `enabled` field is reported as `unsupported`.

## Config

List the current managed runtime config:

```bash
obs-agent-connector config codex list
```

Edit one or more runtime parameters:

```bash
obs-agent-connector config codex edit \
  --enabled=false \
  --endpoint=https://llm-openway.truewatch.com
```

Supported edit parameters:

| Parameter | Description |
| --- | --- |
| `--enabled=<true|false>` | Set runtime enabled state |
| `--endpoint` | Set the OBS / GTrace endpoint |
| `--trace-path` | Set the trace upload path |
| `--metrics-path` | Set the metrics upload path |
| `--x-token` | Set the `X-Token` header |
| `--header` | Add one HTTP header. Supports one or more `--header` parameters |
| `--tag` | Add one resource attribute. Supports one or more `--tag` parameters |
| `--capture-content` | Set content capture mode to `none`, `preview`, or `full` |
| `--max-chars` | Set the maximum captured characters |

Notes:

- `edit` merges the supplied values into the existing config and rewrites the file atomically
- supported Agents: `claude`, `codebuddy`, `codex`, `opencode`, `qoder`, and `workbuddy`
- `hermes` and `openclaw` are excluded because they do not use the managed `gtrace.json` layout

## Install

Install one plugin with stored connector defaults:

```bash
obs-agent-connector install codex
```

Install the default built-in adapters:

```bash
obs-agent-connector install claude
obs-agent-connector install codebuddy
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
On Windows, Claude, CodeBuddy, and Codex register the current connector executable directly. External plugins use their GitHub release PowerShell installer instead of the OSS shell installer.
CodeBuddy, Codex, OpenCode, OpenClaw, Qoder, and WorkBuddy are supported on Windows. Claude is not currently supported on Windows.

When `--agent-id` or `--agent-name` are omitted, the CLI generates them automatically. The default generated `agent_id` uses the format `agid_<uuidv4-without-dashes>`.

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

Updates preserve existing configuration. CodeBuddy reconciles its Hook with the current connector runtime; external plugins receive `--no-config`.
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

- `claude`, `codebuddy`, `codex`, `opencode`, and `qoder` set top-level `enabled`
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

`remove claude` removes the connector-owned `Stop` and `SessionEnd` entries from `~/.claude/settings.json` and preserves unrelated Hooks. `remove codebuddy` removes the connector-owned `Stop` and `SessionEnd` entries from `~/.codebuddy/settings.json` and preserves unrelated Hooks. Add `--purge-config` to also delete the managed `gtrace.json`; for CodeBuddy it also removes `~/.codebuddy/gtrace` upload state.

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

## Uninstall

Uninstall the connector itself:

```bash
obs-agent-connector uninstall
```

Preview only:

```bash
obs-agent-connector uninstall --dry-run
```

Keep connector config:

```bash
obs-agent-connector uninstall --keep-config
```

Behavior:

- removes the current `obs-agent-connector` binary
- removes the managed CodeBuddy Hook while preserving its config and upload state
- removes `~/.obs-agent-connector/config.json` by default
- keeps config when `--keep-config` is used
- removes the installer-managed PATH export from `~/.zshrc`, `~/.bashrc`, or `~/.profile` when found
- removes the connector install directory from the Windows user PATH when present
