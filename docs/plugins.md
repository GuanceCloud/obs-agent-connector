# Plugin Matrix

`obs-agent-connector` delegates final installation and configuration generation to each Agent plugin installer.

## Supported Agents

| Agent | Edition | Installer | Default Config | Default Install Marker |
| --- | --- | --- | --- | --- |
| `claude` | Claude | `https://static.guance.com/claude-otel-plugin/install.sh` | `~/.claude/gtrace.json` | `~/.claude/marketplaces/claude-otel-plugin-release` |
| `codex` | Codex | `https://static.guance.com/codex-otel-plugin/install.sh` | `~/.codex/gtrace.json` | `~/.codex/plugin-sources/codex-otel-plugin/plugins/tracing` |
| `hermes` | Hermes | `https://static.guance.com/hermes-otel-plugin/install.sh` | `~/.hermes/config.yaml` | `~/.hermes/plugins/hermes-otel-plugin` |
| `openclaw` | OpenClaw | `https://static.guance.com/openclaw-otel-plugin/install.sh` | `~/.openclaw/openclaw.json` | `~/.openclaw/extensions/openclaw-otel-plugin` |
| `qoder` | Qoder international edition | `https://static.guance.com/qoder-otel-plugin/install.sh` | `~/.qoder/gtrace.json` | `~/.qoder/plugins/cache/qoder-marketplace/qoder-otel-probe` |
| `qoder-cn` | Qoder China edition | `https://static.guance.com/qoder-otel-plugin/install.sh` | `~/.qoder-cn/gtrace.json` | `~/.qoder-cn/plugins/cache/qoder-marketplace/qoder-otel-probe` |

## Qoder Variants

Both `qoder` and `qoder-cn` use the same plugin installer, but `obs-agent-connector` sets different `QODER_HOME` values:

| Agent | `QODER_HOME` |
| --- | --- |
| `qoder` | `~/.qoder` |
| `qoder-cn` | `~/.qoder-cn` |

This prevents the international and China editions from overwriting each other's plugin files and configuration.

## Install Parameters

The CLI collects these values:

| Prompt | Plugin Argument |
| --- | --- |
| `Endpoint` | `--endpoint` |
| `X-Token` | `--x-token` |
| `Agent ID` | `--tag agent_id=<value>` |
| `Agent Name` | `--tag agent_name=<value>` |

The CLI always uses `--type gtrace`.
