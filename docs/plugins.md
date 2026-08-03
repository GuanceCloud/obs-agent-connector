# Plugin Matrix

`obs-agent-connector` contains optional Claude and Codex telemetry adapters. Their existing external plugins remain the default; `-n` selects the built-in runtime. Other Agents always delegate installation and configuration generation to their external plugin installers.

## Supported Agents

| Agent | Edition | Installer | Default Config | Default Install Marker |
| --- | --- | --- | --- | --- |
| `claude` | Claude | Default: `claude-otel-plugin`; `-n`: current connector | `~/.claude/gtrace.json` | Default: standalone plugin paths; `-n`: managed Hook in `~/.claude/settings.json` |
| `codex` | Codex | Default: `codex-otel-plugin`; `-n`: current connector | `~/.codex/gtrace.json` | Default: standalone plugin paths; `-n`: managed Hook in `~/.codex/hooks.json` |
| `hermes` | Hermes | `https://static.guance.com/hermes-otel-plugin/install.sh` | `~/.hermes/config.yaml` | `~/.hermes/plugins/hermes-otel-plugin` |
| `opencode` | OpenCode with automatic config-directory detection | Unix: `https://static.guance.com/opencode-otel-plugin/opencode-otel-plugin.tar.gz`  Windows: `https://github.com/GuanceCloud/opencode-otel-plugin/releases/latest/download/install-release.ps1` | `~/.config/opencode/gtrace.json` | `~/.config/opencode/plugins/opencode-otel-plugin` |
| `openclaw` | OpenClaw | Unix: `https://static.guance.com/openclaw-otel-plugin/install.sh`  Windows: `https://github.com/GuanceCloud/openclaw-otel-plugin/releases/latest/download/install-release.ps1` | `~/.openclaw/openclaw.json` | `~/.openclaw/extensions/openclaw-otel-plugin` |
| `qoder` | Qoder with automatic CN/global detection | Unix: `https://static.guance.com/qoder-otel-plugin/install.sh`  Windows: `https://github.com/GuanceCloud/qoder-otel-plugin/releases/latest/download/install-release.ps1` | `~/.qoder/gtrace.json` or `~/.qoder-cn/gtrace.json` | `~/.qoder/plugins/cache/qoder-marketplace/qoder-otel-plugin` or `~/.qoder-cn/plugins/cache/qoder-marketplace/qoder-otel-plugin` |
| `workbuddy` | WorkBuddy with automatic profile-directory detection | macOS: `https://static.guance.com/workbuddy-otel-plugin/workbuddy-otel-plugin.tar.gz`  Windows: `https://github.com/GuanceCloud/workbuddy-otel-plugin/releases/latest/download/install-release.ps1` | `~/.workbuddy/gtrace.json` | `~/.workbuddy/plugins/marketplaces/guance/plugins/workbuddy-otel-plugin` |

## Qoder Variants

Both `qoder` and `qoder-cn` use the same plugin installer:

| Agent | Behavior |
| --- | --- |
| `qoder` | Detects the local layout, sets `QODER_HOME`, and passes `--variant cn` or `--variant global` |
| `qoder-cn` | Legacy compatibility target that forces the CN layout with `QODER_HOME=~/.qoder-cn` and `--variant cn` |

Qoder discovery requires an existing `~/.qoder` or `~/.qoder-cn` directory. If neither directory exists, the Agent is treated as not installed and its plugin is not installed.

This prevents the international and China editions from overwriting each other's plugin files and configuration.

## Windows Support

Default-mode Windows plugin installation and update are currently supported only for:

- `codex`
- `opencode`
- `openclaw`
- `qoder`
- `workbuddy`

Claude is additionally supported on Windows with `-n`. New-mode adapters register the current connector executable directly. External plugins download their PowerShell installer from the plugin's GitHub release instead of using the OSS shell installer.
If a user tries `install` or `update` with an unsupported Agent, the CLI returns a friendly error with the supported Windows Agent list.

## Install Parameters

Bootstrap stores shared defaults for `Endpoint` and `X-Token` in `~/.obs-agent-connector/config.json`.
At plugin install time, the CLI uses:

| Value | Source | Plugin Argument |
| --- | --- |
| `Endpoint` | `config.json` or `--endpoint` override | `--endpoint` |
| `X-Token` | `config.json` or `--x-token` override | `--x-token` |
| `Agent ID` | auto-generated `agid_<uuidv4-without-dashes>` or `--agent-id` override | `--tag agent_id=<value>` |
| `Agent Name` | `<hostname>_<agent>_<YYYYMMDD>` or `--agent-name` override | `--tag agent_name=<value>` |

With `-n`, built-in adapters additionally accept `--trace-path`, `--metrics-path`, repeated `--header`, `--capture-content`, `--max-chars`, `--enable`, and `--disable`. These values are merged into the existing `gtrace.json`; unknown fields remain unchanged.

The CLI always uses `--type gtrace`.

## Runtime Toggle

`enable <agent>` and `disable <agent>` change the plugin runtime switch without reinstalling:

| Agent | Updated JSON path |
| --- | --- |
| `claude` | `enabled` |
| `codex` | `enabled` |
| `opencode` | `enabled` |
| `openclaw` | `plugins.entries.openclaw-otel-plugin.enabled` |
| `qoder` | `enabled` |
| `workbuddy` | `enabled` |

`hermes` is not included because its runtime config is `~/.hermes/config.yaml`.
