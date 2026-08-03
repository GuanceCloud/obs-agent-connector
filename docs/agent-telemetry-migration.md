# agent-telemetry Migration

`obs-agent-connector` is the primary repository, executable, and release for the merged telemetry runtime. The new Claude and Codex adapters are opt-in during compatibility migration: existing commands keep using `claude-otel-plugin` and `codex-otel-plugin` unless `-n` is supplied.

## Runtime Mapping

| Previous entry | Connector entry |
| --- | --- |
| `agent-telemetry hook claude` | `obs-agent-connector hook claude` |
| `agent-telemetry hook codex` | `obs-agent-connector hook codex` |
| `agent-telemetry install <adapter>` | `obs-agent-connector install <agent> -n` |
| `agent-telemetry status <adapter>` | `obs-agent-connector status <agent> -n` |
| `agent-telemetry version -u` | `obs-agent-connector version -u` |

The `hook` command is internal and is written to the Agent Hook configuration by the built-in installer.

Keep using `-n` for the full lifecycle of a built-in installation: `list -n`, `status <agent> -n`, `discover -n`, `enable <agent> -n`, `disable <agent> -n`, `update <agent> -n`, and `remove <agent> -n`. Omitting the flag selects the legacy external-plugin view.

## Upgrade Behavior

Installing or updating a built-in adapter:

- replaces managed legacy Hook entries with the current connector executable;
- preserves unrelated Hooks and unknown settings;
- preserves the existing Agent `gtrace.json` unless an install option explicitly changes a field;
- preserves the legacy `gtrace-agent` upload state and Codex `.gtrace` sidecar markers so completed turns are not uploaded again;
- reads `~/.agent-telemetry/config.json` as low-priority bootstrap defaults when connector defaults are missing;
- does not print X-Token values in plans or command previews.

Bootstrap precedence is: explicit install options, existing Agent runtime config, connector defaults, legacy `agent-telemetry` defaults, then built-in defaults.

`update <agent> -n` does not create a per-adapter version. It only reconciles the Hook with the current shared connector runtime. Use `version -u` to upgrade the single connector binary.

## Removal

`remove <agent> -n` removes the managed Hook. Runtime configuration and upload state are kept by default. `--purge-config` removes both the Agent runtime config and adapter upload state.

Removing one built-in adapter never removes the shared connector executable. The top-level `uninstall` command owns connector binary removal.

## Transition

Hermes, OpenCode, OpenClaw, Qoder, and WorkBuddy continue to use external installers during the first migration phase. They will move to `internal/adapters/<product>` without changing the public connector command model.
