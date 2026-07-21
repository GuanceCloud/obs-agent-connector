# Plugin Matrix

`obs-agent-connector` delegates final installation and configuration generation to each Agent plugin installer.

## Supported Agents

| Agent | Edition | Installer | Default Config | Default Install Marker |
| --- | --- | --- | --- | --- |
| `claude` | Claude | `https://static.guance.com/claude-otel-plugin/install.sh` | `~/.claude/gtrace.json` | `~/.claude/marketplaces/claude-otel-plugin-release` |
| `codex` | Codex | Unix: `https://static.guance.com/codex-otel-plugin/install.sh`  Windows: `https://github.com/GuanceCloud/codex-otel-plugin/releases/latest/download/install-release.ps1` | `~/.codex/gtrace.json` | `~/.codex/plugin-sources/codex-otel-plugin/plugins/tracing` |
| `hermes` | Hermes | `https://static.guance.com/hermes-otel-plugin/install.sh` | `~/.hermes/config.yaml` | `~/.hermes/plugins/hermes-otel-plugin` |
| `openclaw` | OpenClaw | Unix: `https://static.guance.com/openclaw-otel-plugin/install.sh`  Windows: `https://github.com/GuanceCloud/openclaw-otel-plugin/releases/latest/download/install-release.ps1` | `~/.openclaw/openclaw.json` | `~/.openclaw/extensions/openclaw-otel-plugin` |
| `qoder` | Qoder with automatic CN/global detection | Unix: `https://static.guance.com/qoder-otel-plugin/install.sh`  Windows: `https://github.com/GuanceCloud/qoder-otel-plugin/releases/latest/download/install-release.ps1` | `~/.qoder/gtrace.json` or `~/.qoder-cn/gtrace.json` | `~/.qoder/plugins/cache/qoder-marketplace/qoder-otel-plugin` or `~/.qoder-cn/plugins/cache/qoder-marketplace/qoder-otel-plugin` |

## Qoder Variants

Both `qoder` and `qoder-cn` use the same plugin installer:

| Agent | Behavior |
| --- | --- |
| `qoder` | Detects the local layout, sets `QODER_HOME`, and passes `--variant cn` or `--variant global` |
| `qoder-cn` | Legacy compatibility target that forces the CN layout with `QODER_HOME=~/.qoder-cn` and `--variant cn` |

Qoder discovery requires an existing `~/.qoder` or `~/.qoder-cn` directory. If neither directory exists, the Agent is treated as not installed and its plugin is not installed.

This prevents the international and China editions from overwriting each other's plugin files and configuration.

## Windows Support

Windows plugin installation and update are currently supported only for:

- `codex`
- `openclaw`
- `qoder`

On Windows, `obs-agent-connector` downloads the plugin PowerShell installer from the plugin's GitHub release instead of using the OSS shell installer.
If a user tries `install` or `update` with an unsupported Agent, the CLI returns a friendly error with the supported Windows Agent list.

## Install Parameters

Bootstrap stores shared defaults for `Endpoint` and `X-Token` in `~/.obs-agent-connector/config.json`.
At plugin install time, the CLI uses:

| Value | Source | Plugin Argument |
| --- | --- |
| `Endpoint` | `config.json` or `--endpoint` override | `--endpoint` |
| `X-Token` | `config.json` or `--x-token` override | `--x-token` |
| `Agent ID` | auto-generated or `--agent-id` override | `--tag agent_id=<value>` |
| `Agent Name` | `<hostname>_<agent>_<YYYYMMDD>` or `--agent-name` override | `--tag agent_name=<value>` |

The CLI always uses `--type gtrace`.

## Runtime Toggle

`enable <agent>` and `disable <agent>` change the plugin runtime switch without reinstalling:

| Agent | Updated JSON path |
| --- | --- |
| `claude` | `enabled` |
| `codex` | `enabled` |
| `openclaw` | `plugins.entries.openclaw-otel-plugin.enabled` |
| `qoder` | `enabled` |

`hermes` is not included because its runtime config is `~/.hermes/config.yaml`.
