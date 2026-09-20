# MiMo external plugin contract

## Scope / Trigger
Native MiMo discovery and lifecycle in obs-agent-connector, using the shared OpenCode telemetry package with a dedicated MiMo entry. Both repository changes must ship together; existing released plugin packages do not imply MiMo support.

## Signatures
`obs-agent-connector install mimo`, `discover`, `status mimo`, `update mimo`, `enable mimo`, `disable mimo`, `remove mimo [--purge-config]`, `config mimo list`.
The plugin installer accepts `--variant mimo` on Unix or `-Variant mimo` on PowerShell. Removal invokes `node <plugin>/scripts/install-config.mjs remove-mimo-config <absolute-config-dir>` before deletion.

## Contracts
Connector passes `MIMOCODE_CONFIG_DIR` resolved from absolute `MIMOCODE_HOME/config` or absolute `XDG_CONFIG_HOME/mimocode` (default `~/.config/mimocode`). Discovery additionally accepts the real data directory or `mimo` on PATH. An empty custom root alone is insufficient. OpenCode paths never imply MiMo installation.
Plugin owns registration and JSONC editing. Registration points to `dist/mimo.js`, which exports one plugin function; `mimo-options.json` persists the independent config directory. Runtime reads MiMo global/project telemetry settings and labels agent_runtime as mimo. Upgrade uses --no-config and preserves settings. Removal must successfully unregister before deleting files; purge removes telemetry settings only.

## Validation & Error Matrix
- Relative MIMOCODE_HOME/config override: reject; relative XDG: use native default.
- Malformed/unreadable runtime metadata: fail open with empty hooks; no credential-bearing error log.
- Missing unregistration helper or helper failure: stop removal; preserve plugin files/config for repair.
- New credential JSON files: mode 0600; retain existing modes on update.
- Old package missing variant support: install fails rather than silently configuring OpenCode.

## Good / Base / Bad Cases
Base: default ~/.config/mimocode with executable detected.
Good: absolute custom root, existing JSONC/providers/unrelated plugins survive full lifecycle.
Bad: an OpenCode-only installation or nonexistent MiMo roots must not be discovered as MiMo.

## Tests Required
Path precedence, platform args, discovery negative cases; upgrade config byte preservation; failed cleanup aborts deletion; default/purge retain host auth/provider data; malformed runtime metadata fail-open; runtime host isolation; OpenCode regressions. Native macOS lifecycle tested from packaged archive and real MiMo conversation exported both signals. Windows is statically reviewed/cross-platform arguments tested, not executed natively.

## Wrong vs Correct
Wrong: set OPENCODE_HOME to MiMo and assume runtime config follows automatically.
Correct: explicit host variant, native registration, independent runtime config, and plugin-owned cleanup.
Wrong: delete plugin folder while retaining its file URL in MiMo config.
Correct: remove only the owned registration before deleting managed files.
