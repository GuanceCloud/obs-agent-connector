# Plugin Matrix

`obs-agent-connector` contains built-in adapters for Claude, Claude Desktop, CodeBuddy, Codex, Cursor, Deep Agents Code, Grok Build, Kiro CLI, OMP, Pi, QwenWork CN, and WorkBuddy. Other Agents delegate installation and configuration generation to external plugin installers.

Built-in `invoke_agent` spans do not carry token usage. Attributable token usage remains on `llm` spans and drives token metrics. Kiro's aggregate billing credit remains on `invoke_agent` because it has no per-call attribution. Turn-only token usage without supported per-call attribution is omitted from exported spans.

## Supported Agents

| Agent | Edition | Installer | Default Config | Default Install Marker |
| --- | --- | --- | --- | --- |
| `claude` | Claude | Current connector | `~/.obs-agent-connector/claude/gtrace.json` | Managed Hooks in `~/.claude/settings.json` |
| `claude-desktop` | Claude Desktop local Code and Cowork on macOS, Linux, and Windows | Current connector | `~/.obs-agent-connector/claude-desktop/gtrace.json` | OBS machine policy drop-in and runtime |
| `codebuddy` | Tencent Cloud CodeBuddy / WorkBuddy Enterprise IDE Agent | Current connector | `~/.obs-agent-connector/codebuddy/gtrace.json` | Managed Hook in `~/.codebuddy/settings.json` |
| `codex` | Codex | Current connector | `~/.obs-agent-connector/codex/gtrace.json` | Managed Hook and trust state in `$CODEX_HOME/hooks.json` / `$CODEX_HOME/config.toml` (defaults to `~/.codex`) |
| `cursor` | Cursor with automatic `~/.cursor` or Cursor CLI-family detection, preferring `cursor-agent` | Current connector | `~/.obs-agent-connector/cursor/gtrace.json` | Managed Hooks in `~/.cursor/hooks.json` |
| `dcode` | Deep Agents Code with Hooks v2 (`dcode` 0.1.46 or later); normal `Stop` plus failed `SessionEnd` terminal telemetry | Current connector | `~/.obs-agent-connector/dcode/gtrace.json` | Managed Hooks in `~/.deepagents/hooks.json` |
| `grok` | Grok Build CLI 1.0.5+ TUI/headless | Current connector | `~/.obs-agent-connector/grok/gtrace.json` | Managed global Hook in `~/.grok/hooks/obs-agent-connector.json` |
| `kiro` | Kiro CLI V3 interactive TTY (`kiro-cli chat --v3`); default V2 and non-interactive modes are unsupported | Current connector | `~/.obs-agent-connector/kiro/gtrace.json` | Managed V3 global Hooks in `~/.kiro/hooks/obs-agent-connector.json` |
| `omp` | Oh My Pi 18.1.15+ | Current connector | `~/.obs-agent-connector/omp/gtrace.json` | `~/.omp/agent/extensions/obs-agent-connector.js` |
| `pi` | Pi Coding Agent 0.85.1+ | Current connector | `~/.obs-agent-connector/pi/gtrace.json` | Managed extension registered in `~/.pi/agent/settings.json` |
| `dsh` | DeepSeek Harness | Unix: `https://static.guance.com/agent_plugins/dsh-otel-plugin/install.sh` Windows: `https://static.guance.com/agent_plugins/dsh-otel-plugin/install-release.ps1` | `$DSH_HOME/gtrace.json` (default `~/.dsh/gtrace.json`) | `$DSH_HOME/profiles/<profile>/node_modules/dsh-otel-plugin` |
| `hermes` | Hermes | `https://static.guance.com/agent_plugins/hermes-otel-plugin/install.sh` | `$HERMES_HOME/config.yaml` (default `~/.hermes/config.yaml`) | `$HERMES_HOME/plugins/hermes-otel-plugin` |
| `opencode` | OpenCode with automatic config-directory detection | Unix: `https://static.guance.com/agent_plugins/opencode-otel-plugin/opencode-otel-plugin.tar.gz`  Windows: `https://static.guance.com/agent_plugins/opencode-otel-plugin/install-release.ps1` | `~/.config/opencode/gtrace.json` | `~/.config/opencode/plugins/opencode-otel-plugin` |
| `mimo` | MiMo Code using the host-aware OpenCode plugin with `--variant mimo` | Same package and release installer as `opencode` (requires a host-aware release) | `~/.config/mimocode/gtrace.json` | `~/.config/mimocode/plugins/opencode-otel-plugin` |
| `openclaw` | OpenClaw | Unix: `https://static.guance.com/agent_plugins/openclaw-otel-plugin/install.sh`  Windows: `https://static.guance.com/agent_plugins/openclaw-otel-plugin/install-release.ps1` | `~/.openclaw/openclaw.json` | `~/.openclaw/extensions/openclaw-otel-plugin` |
| `qoder` | Qoder with automatic CN/global detection | Unix: `https://static.guance.com/agent_plugins/qoder-otel-plugin/qoder-otel-plugin.tar.gz`  Windows: `https://static.guance.com/agent_plugins/qoder-otel-plugin/install-release.ps1` | `~/.qoder/gtrace.json` or `~/.qoder-cn/gtrace.json` | `~/.qoder/plugins/cache/qoder-marketplace/qoder-otel-plugin` or `~/.qoder-cn/plugins/cache/qoder-marketplace/qoder-otel-plugin` |
| `workbuddy` | WorkBuddy with automatic profile-directory detection | Built into `obs-agent-connector` | `~/.obs-agent-connector/workbuddy/gtrace.json` (legacy profile config remains readable) | `<profile>/settings.json` managed Hooks |
| `qwenwork-cn` | QwenWork CN domestic desktop on macOS, Linux and Windows; Windows AMD64 user smoke test passed, Linux/Windows ARM64 runtime unverified; [local Hook contract](product-research/qwenwork-cn.md) | Built into `obs-agent-connector` | `~/.obs-agent-connector/qwenwork-cn/gtrace.json` | `~/.qwenworkcn/settings.json` managed Hooks |

## Claude Desktop

### Managed registration

Run these commands from the account that uses Claude Desktop. OBS requests
administrator authorization as needed for install, update, and removal. It uses
sudo on macOS and Linux and a UAC prompt on Windows. `--yes` skips the OBS
confirmation. Start a new local Code or Cowork session after installing or updating.

```bash
obs-agent-connector install claude-desktop
obs-agent-connector status claude-desktop
obs-agent-connector update claude-desktop
obs-agent-connector remove claude-desktop
```

The machine registration uses the following directories.

| Platform | Machine directory | Runtime |
| --- | --- | --- |
| macOS | `/Library/Application Support/ClaudeCode` | `obs-agent-connector-claude-desktop` |
| Linux | `/etc/claude-code` | `obs-agent-connector-claude-desktop` |
| Windows | `%ProgramFiles%\ClaudeCode` | `obs-agent-connector-claude-desktop.exe` |

Within the machine directory, OBS writes
`managed-settings.d/50-obs-claude-desktop.json`. The first file-based deployment
opts into merging Claude Desktop's host policy. The CLI reports conflicts with
known device policy, a custom `policyHelper`, or file settings that disable
Hooks or prevent the required first-policy merge. Status reports machine-file
presence and the current account's telemetry setting. `list` shows the
policy path for a verified machine registration.
[Anthropic managed settings documentation](https://code.claude.com/docs/en/managed-settings)

Check a new local Code session and a new Cowork session to confirm Claude loads
the policy. Native Linux and Windows validation is pending for the RC. Windows
Cowork requires the Cowork service included in Anthropic's full Desktop
installer and a compatible Windows device.
[Anthropic Windows deployment requirements](https://support.claude.com/en/articles/12622703-deploy-claude-desktop-for-windows)

The Hook runs as the OS account running the Desktop session and requires `enabled: true` in that
account's Desktop `gtrace.json`. OBS writes this file with private account permissions.
Additional accounts can configure their own
destination and enable telemetry without administrator authorization after the
machine registration is installed.

```bash
obs-agent-connector config claude-desktop edit --enabled=true \
  --endpoint=https://llm-openway.guance.com --x-token=agent_xxx
```

`update claude-desktop` refreshes the machine files from the current OBS binary.
`remove claude-desktop` deletes the machine files and keeps the user configuration
and Claude upload ledger.
Machine Hooks with an enabled user configuration can continue collecting after a
plain `uninstall`. Run `remove claude-desktop` before uninstalling the
connector when decommissioning the machine registration.

### Telemetry

The machine Hooks run on `Stop` and `SessionEnd`. Recognized Desktop sessions
use `agent_runtime=claude-desktop` and a `claude_surface` resource attribute of
`code` or `cowork`. Terminal Claude CLI sessions use `agent_runtime=claude`.
The adapter reads only the Desktop configuration for its endpoint,
headers, enabled state, and content capture. Use `disable claude-desktop` to stop
Desktop exports for the current account.

An existing `claude` installation and the Desktop machine Hook can handle the
same local Code turn. Both adapters use the Claude upload ledger, including a
`state_dir` override from the existing Claude configuration. The ledger locks each
session and turn and records trace and metrics success separately.
Configure both adapters for the same destination when they handle the same
Code sessions. If the legacy Hook uploads first, it labels the recognized
Desktop session as `claude-desktop` and uses the legacy Hook's configuration.

Removing the legacy `claude` adapter while Desktop Hooks are present preserves
the shared upload ledger, including any `state_dir` override from a discarded
Claude config.

## Hermes Home Directory

Hermes uses a non-empty `HERMES_HOME` for plugin discovery, installed-version detection,
configuration lookup, installation, updates, and removal. An unset or empty value
falls back to `~/.hermes`. A leading `~/` is expanded before passing the same home
to the plugin installer.

`discover` detects the Hermes command on PATH. If the command is unavailable to
the connector process, an existing `config.yaml` or installed plugin under the
selected Hermes home also identifies the target (`COMMAND` is `data-dir`). An
empty directory or an environment variable alone is not enough. A custom home
never falls back to another user's default profile.

```bash
export HERMES_HOME="$HOME/custom-hermes"
obs-agent-connector install hermes
obs-agent-connector update hermes
obs-agent-connector remove hermes
```

Updates preserve existing configuration with `--no-config`. Removal deletes only
the plugin under the selected home and keeps `config.yaml` unless `--purge-config`
is supplied. Use the same `HERMES_HOME` for each command.

## WorkBuddy Migration

For WorkBuddy registration, legacy-plugin migration, and runtime limitations, see
[the built-in WorkBuddy adapter](product-research/workbuddy.md).

## Grok Build

The Grok adapter uses a hybrid journal and transcript-replay design. Hook handlers return quickly after recording bounded evidence. A detached worker uses the matching `updates.jsonl` terminal record to normalize and upload a completed turn.

- `Stop` is a blocking gate and may fire repeatedly. The connector requires a matching durable `TurnCompleted` record, so a blocked or repeated `Stop` does not upload a partial turn.
- `StopFailure` and `StopCancelled` preserve explicit failure or cancellation evidence. A later `UserPromptSubmit`, `idle_prompt` notification, or `SessionEnd` also recovers terminal turns that did not receive a final observable Stop event.
- Exact per-call model and token fields are emitted when `ResponseStarted` and `ResponseCompleted` provide stable call evidence. When Grok exposes only a complete multi-call turn aggregate, the connector conservatively apportions it across the validated LLM calls, preserves the exact total, and marks each allocation with `gtrace.usage.estimated=true`.
- Skill and subagent spans require stable IDs or a high-confidence `SKILL.md` path. The connector does not infer relationships from timing alone.
- Grok's native External OpenTelemetry stream may run at the same time. It exports logs and metrics, while the connector provides GTrace traces and derived metrics; enabling both can produce overlapping telemetry volume.

The connector owns `~/.grok/hooks/obs-agent-connector.json` and `~/.obs-agent-connector/grok/`. It preserves other global and project Hook files. Restart Grok after installation, or run `/hooks`, select the Hooks tab, and press `l` to reload.

See [Grok Build telemetry product research](product-research/grok.md) for schema and validation details.

## OMP

OMP uses a bundled native extension and a built-in Go worker. Fully exit and restart the OMP process after installing or updating; `/reload-plugins` does not reload this JavaScript extension. See [OMP integration](product-research/omp.md) for profiles, event semantics, validation, and limitations.

## Pi

Pi uses a native lifecycle extension registered through the global `extensions` setting. The connector preserves unrelated settings and extensions, and `/reload` or a new session loads an installed update. A terminal snapshot is handed to one bounded built-in Go queue worker only on `agent_settled`. The adapter derives LLM duration from `before_provider_request` through assistant `message_end`, derives TTFT from the first streaming `message_update`, and omits TTFT when no qualifying stream event exists. The official Subagent extension is observed as a regular `subagent` tool; independently launched child Pi processes produce separate traces and are not linked to the parent trace. See [Pi telemetry product research](product-research/pi.md).

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
- `claude-desktop`
- `codex`
- `cursor`
- `dcode`
- `grok`
- `kiro`
- `omp`
- `pi`
- `codebuddy`
- `dsh`
- `mimo`
- `opencode`
- `openclaw`
- `qoder`
- `qwenwork-cn`
- `workbuddy`

Claude, CodeBuddy, Codex, Cursor, Deep Agents Code, Grok Build, Kiro, OMP, Pi, QwenWork CN, and WorkBuddy register the current connector executable directly. Claude Desktop registers a machine-level policy and runtime with administrator authorization. External plugins download their PowerShell installer from the configured OSS or GitHub source.
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

The built-in Claude, Claude Desktop, CodeBuddy, Codex, Cursor, Deep Agents Code, Grok Build, Kiro, OMP, Pi, QwenWork CN, and WorkBuddy adapters accept `--trace-path`, `--metrics-path`, one or more `--header` parameters, one or more `--tag` parameters, `--capture-content`, `--max-chars`, `--enable`, and `--disable`. Values are merged into the existing `gtrace.json`, and unknown fields remain unchanged.

Each built-in adapter writes structured Hook logs to `~/.obs-agent-connector/<agent>/gtrace-hooks.json`. For adapters with a legacy configuration layout, Agent-local configs are read as a compatibility fallback and are migrated into the managed directory when an install or config edit writes new values.

The CLI always uses `--type gtrace`.

## External Plugin Installer Contract

All external OTEL plugins follow one installer contract. The connector downloads
the platform installer and passes only the common telemetry arguments (`latest`,
`--type gtrace`, endpoint, token, tags, and plugin-specific options). The
installer is responsible for resolving its own release archive, verifying its
checksum, installing into the Agent profile, and merging Agent-local runtime
configuration. The connector must not inject `--source`, construct a plugin
archive URL, or write an external plugin's private configuration.

This keeps OSS and GitHub Release delivery interchangeable and makes Unix and
Windows installers equivalent. New external plugins should implement
`install.sh`, `install-release.sh`, and `install-release.ps1` against this
contract and add a regression test for the generated command.

## Runtime Toggle

`enable <agent>` and `disable <agent>` change the plugin runtime switch without reinstalling:

| Agent | Updated JSON path |
| --- | --- |
| `claude` | `enabled` |
| `claude-desktop` | `enabled` |
| `codebuddy` | `enabled` |
| `codex` | `enabled` |
| `cursor` | `enabled` |
| `dcode` | `enabled` |
| `grok` | `enabled` |
| `kiro` | `enabled` |
| `omp` | `enabled` |
| `pi` | `enabled` |
| `dsh` | `enabled` |
| `mimo` | `enabled` |
| `opencode` | `enabled` |
| `openclaw` | `plugins.entries.openclaw-otel-plugin.enabled` |
| `qoder` | `enabled` |
| `workbuddy` | `enabled` |
| `qwenwork-cn` | `enabled` |

`hermes` is not included because its runtime config is `$HERMES_HOME/config.yaml` (default `~/.hermes/config.yaml`).

CodeBuddy accepts both legacy IDE `index.json` plus `messages/` transcripts and CodeBuddy Code CLI JSONL sessions through the same managed Hooks. No conversion script or version-specific configuration is required. See [CodeBuddy transcript semantics](product-research/codebuddy.md) for terminal states, token deduplication, and timing limits.
