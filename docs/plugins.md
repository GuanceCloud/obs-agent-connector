# Plugin Matrix

`obs-agent-connector` contains built-in adapters for Claude, CodeBuddy, Codex, and Cursor. Other Agents delegate installation and configuration generation to external plugin installers.

## Supported Agents

| Agent | Edition | Installer | Default Config | Default Install Marker |
| --- | --- | --- | --- | --- |
| `claude` | Claude | Current connector | `~/.obs-agent-connector/claude/gtrace.json` | Managed Hooks in `~/.claude/settings.json` |
| `codebuddy` | Tencent Cloud CodeBuddy / WorkBuddy Enterprise IDE Agent | Current connector | `~/.obs-agent-connector/codebuddy/gtrace.json` | Managed Hook in `~/.codebuddy/settings.json` |
| `codex` | Codex | Current connector | `~/.obs-agent-connector/codex/gtrace.json` | Managed Hook and trust state in `~/.codex/hooks.json` / `~/.codex/config.toml` |
| `cursor` | Cursor with automatic `~/.cursor` or Cursor CLI-family detection, preferring `cursor-agent` | Current connector | `~/.obs-agent-connector/cursor/gtrace.json` | Managed Hooks in `~/.cursor/hooks.json` |
| `dsh` | DeepSeek Harness | Unix: `https://static.guance.com/dsh-otel-plugin/install.sh` Windows: `https://github.com/GuanceCloud/dsh-otel-plugin/releases/latest/download/install-release.ps1` | `$DSH_HOME/gtrace.json` (default `~/.dsh/gtrace.json`) | `$DSH_HOME/profiles/<profile>/node_modules/dsh-otel-plugin` |
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

Windows installation and update are currently supported only for:

- `claude`
- `codex`
- `cursor`
- `codebuddy`
- `dsh`
- `opencode`
- `openclaw`
- `qoder`
- `workbuddy`

Claude, CodeBuddy, Codex, and Cursor register the current connector executable directly. External plugins download their PowerShell installer from the plugin's GitHub release instead of using the OSS shell installer.
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

The built-in Claude, CodeBuddy, Codex, and Cursor adapters accept `--trace-path`, `--metrics-path`, one or more `--header` parameters, one or more `--tag` parameters, `--capture-content`, `--max-chars`, `--enable`, and `--disable`. Values are merged into the existing `gtrace.json`, and unknown fields remain unchanged.

Each built-in adapter writes structured Hook logs to `~/.obs-agent-connector/<agent>/gtrace-hooks.json`. Existing Agent-local configs are read as a compatibility fallback and are migrated into the managed directory when an install or config edit writes new values.

The CLI always uses `--type gtrace`.

## Runtime Toggle

`enable <agent>` and `disable <agent>` change the plugin runtime switch without reinstalling:

| Agent | Updated JSON path |
| --- | --- |
| `claude` | `enabled` |
| `codebuddy` | `enabled` |
| `codex` | `enabled` |
| `cursor` | `enabled` |
| `dsh` | `enabled` |
| `opencode` | `enabled` |
| `openclaw` | `plugins.entries.openclaw-otel-plugin.enabled` |
| `qoder` | `enabled` |
| `workbuddy` | `enabled` |

`hermes` is not included because its runtime config is `~/.hermes/config.yaml`.
