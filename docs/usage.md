# obs-agent-connector Usage Guide

## Overview

`obs-agent-connector` is a CLI for installing, discovering, updating, enabling, disabling, and removing OBS / GTrace plugins across supported AI Agents.

Supported Agents:

- `claude`
- `codex`
- `hermes`
- `openclaw`
- `qoder`

Notes:

- `qoder` automatically detects global vs CN layouts
- Windows currently supports `codex`, `openclaw`, and `qoder` only

## Install obs-agent-connector

### macOS / Linux

Recommended bootstrap command:

```bash
curl -fsSL -O https://static.guance.com/obs-agent-connector/install.sh && \
sh install.sh \
  --endpoint=https://llm-openway.guance.com \
  --x-token=agent_xxx
```

What the installer does:

- downloads the correct binary for the current platform
- verifies `SHA256SUMS`
- writes `~/.obs-agent-connector/config.json`
- updates `PATH` when possible
- removes the downloaded `install.sh` after installation

### Windows

PowerShell example:

```powershell
Invoke-WebRequest -Uri "https://static.guance.com/obs-agent-connector/install.ps1" -OutFile "install.ps1"
.\install.ps1 -Endpoint "https://llm-openway.guance.com" -XToken "agent_xxx"
```

## Connector Config

Default config file:

```text
~/.obs-agent-connector/config.json
```

Typical content:

```json
{
  "download_base_url": "https://static.guance.com/obs-agent-connector",
  "plugin_source": "oss",
  "plugin_base_url": "https://static.guance.com",
  "endpoint": "https://llm-openway.guance.com",
  "x_token": "agent_xxx"
}
```

Field reference:

| Field | Description |
| --- | --- |
| `download_base_url` | Download base URL for the connector itself, including metadata and binary packages |
| `plugin_source` | Agent plugin source, currently `oss` or `github` |
| `plugin_base_url` | Base URL used for Agent plugin downloads |
| `endpoint` | OBS / GTrace ingest endpoint |
| `x_token` | Authentication token |

Behavior notes:

- reinstalling the connector overwrites the old `config.json`
- if `plugin_source` is omitted, it defaults to `oss`

## Installer Script Parameters

### `install.sh`

```bash
install.sh [--version <tag|latest>] \
           [--install-dir <path>] \
           [--config-dir <path>] \
           [--download-base-url <url>] \
           [--endpoint <url>] \
           [--x-token <token>] \
           [--plugin-source <oss|github>] \
           [--plugin-base-url <url>] \
           [--binary-only]
```

Parameters:

| Parameter | Description |
| --- | --- |
| `--version` | Install a specific connector version. Default: `latest` |
| `--install-dir` | Directory for the connector binary |
| `--config-dir` | Config directory. Default: `~/.obs-agent-connector` |
| `--download-base-url` | Download base for the connector binary and metadata |
| `--endpoint` | OBS / GTrace endpoint |
| `--x-token` | Authentication token |
| `--plugin-source` | Plugin source, `oss` or `github` |
| `--plugin-base-url` | Base URL for plugin downloads |
| `--binary-only` | Update the binary only and skip writing `config.json` |

Default behavior:

- if `--download-base-url` is omitted, it is derived from `--endpoint`  
  Example: `https://llm-openway.guance.com` -> `https://static.guance.com/obs-agent-connector`
- if `--plugin-source` is omitted, the default is `oss`
- if `plugin_source=oss` and `--plugin-base-url` is omitted, the value is derived from `download_base_url`

### `install.ps1`

```powershell
install.ps1 [-Version <tag|latest>] `
            [-InstallDir <path>] `
            [-ConfigDir <path>] `
            [-DownloadBaseUrl <url>] `
            [-PluginSource <oss|github>] `
            [-PluginBaseUrl <url>] `
            [-Endpoint <url>] `
            [-XToken <token>] `
            [-BinaryOnly] `
            [-NoPathUpdate]
```

Additional parameter:

| Parameter | Description |
| --- | --- |
| `-NoPathUpdate` | Skip updating the user PATH |

## Command Summary

```bash
obs-agent-connector list
obs-agent-connector discover
obs-agent-connector discover -u
obs-agent-connector install codex
obs-agent-connector update codex
obs-agent-connector enable codex
obs-agent-connector disable codex
obs-agent-connector remove codex
obs-agent-connector version
obs-agent-connector version -u
```

## `list`

Show installed plugins:

```bash
obs-agent-connector list
```

The output includes:

- Agent name
- plugin version, when it can be detected
- runtime config path
- installed plugin path

If the version cannot be derived from the local layout or plugin manifest, the version column shows `-`.

## `discover`

### Install missing plugins

```bash
obs-agent-connector discover
```

### Sync all detected Agents

This mode:

- updates plugins that are already installed
- installs plugins that are missing

```bash
obs-agent-connector discover -u
```

Also supported:

```bash
obs-agent-connector discover --update
```

### Parameters

| Parameter | Description |
| --- | --- |
| `--endpoint` | Override the configured endpoint for this run |
| `--x-token` | Override the configured token for this run |
| `--static-base` | Override the plugin download base URL for this run |
| `--yes` | Skip confirmation |
| `--dry-run` | Print the plan only |
| `-u`, `--update` | Update installed plugins and install missing plugins in one pass |

### Behavior

- discovers supported local Agents
- installs missing plugins by default
- switches to sync mode when `-u` or `--update` is used
- auto-generates:
  - `agent_id` as `agid_<32 lowercase hex chars>`
  - `agent_name` as `<hostname>_<agent>_<YYYYMMDD>`
- shows detected plugin versions in the output
- only includes `qoder` when `~/.qoder` or `~/.qoder-cn` already exists

Example:

```bash
obs-agent-connector discover \
  --endpoint https://llm-openway.guance.com \
  --x-token agent_xxx \
  --yes
```

## `install`

Install a single Agent plugin:

```bash
obs-agent-connector install codex
```

Parameters:

| Parameter | Description |
| --- | --- |
| `--endpoint` | Set the OBS / GTrace endpoint |
| `--x-token` | Set the authentication token |
| `--agent-id` | Override the generated `agent_id` |
| `--agent-name` | Override the generated `agent_name` |
| `--static-base` | Override the plugin download base URL |
| `--yes` | Skip confirmation |
| `--dry-run` | Print the install plan and command preview only |

Example:

```bash
obs-agent-connector install codex \
  --endpoint https://llm-openway.guance.com \
  --x-token agent_xxx \
  --agent-id agid_550e8400e29b41d4a716446655440000 \
  --agent-name prod_codex \
  --yes
```

Notes:

- if `--agent-id` is omitted, the CLI generates `agid_<uuidv4-without-dashes>`
- if `--agent-name` is omitted, the CLI generates `<hostname>_<agent>_<YYYYMMDD>`
- `install` accepts a single Agent target only

## `update`

Update a single installed plugin:

```bash
obs-agent-connector update codex
```

Parameters:

| Parameter | Description |
| --- | --- |
| `--static-base` | Override the plugin download base URL for this run |
| `--yes` | Skip confirmation |
| `--dry-run` | Print the update plan and command preview only |

Notes:

- `update` accepts a single Agent target only
- the command preserves the existing runtime config
- internally it passes `--no-config` to the plugin installer

## `enable` / `disable`

Enable a plugin:

```bash
obs-agent-connector enable codex
```

Disable a plugin:

```bash
obs-agent-connector disable codex
```

Parameters:

| Parameter | Description |
| --- | --- |
| `--dry-run` | Print the config change without writing it |

Notes:

- these commands update the Agent runtime config in place
- currently supported:
  - `claude`
  - `codex`
  - `qoder`
  - `openclaw`
- `hermes` is not supported because its runtime config is YAML, not the JSON `enabled` structure handled by this CLI

## `remove`

Remove a plugin:

```bash
obs-agent-connector remove codex
```

Remove the plugin and its runtime config:

```bash
obs-agent-connector remove codex --purge-config
```

Parameters:

| Parameter | Description |
| --- | --- |
| `--yes` | Skip confirmation |
| `--dry-run` | Print the removal plan only |
| `--purge-config` | Also remove the plugin config file |

Notes:

- by default the plugin is removed and the config is kept
- `remove` accepts a single Agent target only

## `version`

Show the current version and check the latest release:

```bash
obs-agent-connector version
```

Run self-update immediately:

```bash
obs-agent-connector version -u
```

Show local version only:

```bash
obs-agent-connector version --offline
```

Parameters:

| Parameter | Description |
| --- | --- |
| `-u` | Update the connector to the latest release |
| `--offline` | Skip remote release checks |

Notes:

- `version` reads `download_base_url` from `config.json`
- for OSS installs, the latest version is read from `latest.txt`
- for GitHub installs, the latest version is resolved through the GitHub Releases latest API so version checks are not pinned to an old tag
- `version -u` downloads and replaces the current platform binary

## Windows Support

Supported Agents on Windows:

- `codex`
- `openclaw`
- `qoder`

Notes:

- Windows does not use the OSS shell installer path for plugin installation
- it uses each plugin's PowerShell installer
- unsupported Agents return a friendly error

## Qoder Notes

`qoder` automatically detects the local layout:

| Layout | Directory |
| --- | --- |
| Global | `~/.qoder` |
| China | `~/.qoder-cn` |

Behavior:

- install uses the correct `--variant global` or `--variant cn`
- discovery skips `qoder` until one of the directories exists
- `qoder-cn` is kept as a compatibility target, but `qoder` is the preferred public target

## Common Examples

### Initialize the connector and save shared defaults

```bash
curl -fsSL -O https://static.guance.com/obs-agent-connector/install.sh && \
sh install.sh \
  --endpoint=https://llm-openway.guance.com \
  --x-token=agent_xxx
```

### Install missing plugins for detected Agents

```bash
obs-agent-connector discover
```

### Sync all detected Agents

```bash
obs-agent-connector discover -u --yes
```

### Install one plugin

```bash
obs-agent-connector install qoder --yes
```

### Update one plugin

```bash
obs-agent-connector update codex --yes
```

### Disable a plugin

```bash
obs-agent-connector disable codex
```

### Remove a plugin but keep its config

```bash
obs-agent-connector remove codex --yes
```

### Check version and update the connector itself

```bash
obs-agent-connector version
obs-agent-connector version -u
```

## Additional Notes

- runtime config content is owned by each Agent plugin installer, not generated directly by `obs-agent-connector`
- plugin version detection is best-effort; if the plugin layout changes, the version column may show `-`
- the installer verifies package integrity and stops on checksum failures
