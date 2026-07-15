# Plugin Matrix

`obs-agent-connector` delegates final installation and configuration generation to each Agent plugin installer.

## Supported Agents

| Agent | Edition | Installer | Default Config | Default Install Marker |
| --- | --- | --- | --- | --- |
| `claude` | Claude | `https://static.guance.com/claude-otel-plugin/install.sh` | `~/.claude/gtrace.json` | `~/.claude/marketplaces/claude-otel-plugin-release` |
| `codex` | Codex | `https://static.guance.com/codex-otel-plugin/install.sh` | `~/.codex/gtrace.json` | `~/.codex/plugin-sources/codex-otel-plugin/plugins/tracing` |
| `hermes` | Hermes | `https://static.guance.com/hermes-otel-plugin/install.sh` | `~/.hermes/config.yaml` | `~/.hermes/plugins/hermes-otel-plugin` |
| `openclaw` | OpenClaw | `https://static.guance.com/openclaw-otel-plugin/install.sh` | `~/.openclaw/openclaw.json` | `~/.openclaw/extensions/openclaw-otel-plugin` |
| `qoder` | Qoder with automatic CN/global detection | `https://static.guance.com/qoder-otel-plugin/install.sh` | `~/.qoder/gtrace.json` or `~/.qoder-cn/gtrace.json` | `~/.qoder/plugins/cache/qoder-marketplace/qoder-otel-plugin` or `~/.qoder-cn/plugins/cache/qoder-marketplace/qoder-otel-plugin` |

## Qoder Variants

Both `qoder` and `qoder-cn` use the same plugin installer:

| Agent | Behavior |
| --- | --- |
| `qoder` | Detects the local layout, sets `QODER_HOME`, and passes `--variant cn` or `--variant global` |
| `qoder-cn` | Legacy compatibility target that forces the CN layout with `QODER_HOME=~/.qoder-cn` and `--variant cn` |

Qoder discovery requires an existing `~/.qoder` or `~/.qoder-cn` directory. If neither directory exists, the Agent is treated as not installed and its plugin is not installed.

This prevents the international and China editions from overwriting each other's plugin files and configuration.

## Install Parameters

Bootstrap stores shared defaults for `Endpoint` and `X-Token` in `~/.obs-agent-connector/config.json`.
At plugin install time, the CLI uses:

| Value | Source | Plugin Argument |
| --- | --- |
| `Endpoint` | `config.json` or `--endpoint` override | `--endpoint` |
| `X-Token` | `config.json` or `--x-token` override | `--x-token` |
| `Agent ID` | auto-generated or `--agent-id` override | `--tag agent_id=<value>` |
| `Agent Name` | auto-generated or `--agent-name` override | `--tag agent_name=<value>` |

The CLI always uses `--type gtrace`.
