# WorkBuddy built-in adapter

## Evidence and architecture

The adapter replaces the external `workbuddy-otel-plugin` runtime with Go code in
`internal/adapters/workbuddy`. The reference is the external plugin's WorkBuddy
5.2.6 JSONL fixture, Hook manifest, parser, and installer. This is not the CodeBuddy
`index.json` format and the products do not share a parser.

WorkBuddy supports command Hooks in its profile `settings.json`. The managed
command is `obs-agent-connector hook workbuddy --profile <profile>`. No Node.js,
marketplace download, or separate plugin runtime is required.

- `UserPromptSubmit`, tool before/after/failure, and subagent events record metadata.
- `Stop`, `StopFailure`, `SessionEnd`, and `SubagentStop` queue a background worker.
- The worker waits for a stable JSONL snapshot and replays terminal turns.
- Tool timing uses call IDs, with ordered argument-hash matching when IDs are absent.
- LLM batches use output and tool-result boundaries; timing is explicitly inferred.
- Usage comes from `message.usage` or `providerData.usage`; absent usage is not estimated.
- Skills require a real `SKILL.md` or a named Skill call resolving to an existing file.
- Explicit parent session/tool IDs remain span attributes, not resource dimensions.
- Shared core builders generate spans and metrics. Only LLM spans carry token usage.

## Installation and migration

The supported product platforms remain macOS and Windows. Discovery uses
`WORKBUDDY_CONFIG_DIR`, `CODEBUDDY_CONFIG_DIR`, or `~/.workbuddy`.

Installation merges command Hooks without dropping unrelated handlers in the same
group. It disables `workbuddy-otel-plugin@guance` and removes only that entry from
the installed-plugin registry. Existing plugin files, marketplace entries, and
legacy configuration are retained. The managed configuration is
`~/.obs-agent-connector/workbuddy/gtrace.json`; legacy `<profile>/gtrace.json` is a
fallback. `update` uses `--no-config` semantics and leaves both files unchanged.

The installer does not refuse solely because an application process exists.
Restart WorkBuddy before the next conversation after migration, so an already
loaded legacy plugin stops running. If the application overwrites settings on
exit, rerun installation while it is closed. Live reload and configuration
writeback behavior still require native macOS/Windows product validation; built-in
packaging alone does not guarantee a restart-free install.

## Reliability and privacy

Hook failures do not fail the Agent operation. Journals contain event metadata and
argument hashes, not prompts or raw tool input. Background queues persist on
failure and are retried by later terminal Hooks. Upload claims and success markers
are per turn and per signal. Existing legacy completion and signal markers are
honored to avoid replay after migration; an active legacy upload defers the turn.
Legacy workers already loaded in a running application cannot be forcibly unloaded
by editing settings, hence the migration restart requirement.

Managed logs and state live under `~/.obs-agent-connector/workbuddy/`. Content is
recursively redacted and bounded; `captureContent=none` retains structural and
usage data without message or tool bodies. Legacy boolean `capture_content` and
snake-case limits remain supported.

## Validation and limitations

Tests cover synthetic WorkBuddy-format turns, tool timing/failure, usage, skills,
explicit subagent associations, privacy, incomplete transcript filtering, terminal
errors/cancellation, installation migration, config preservation, unrelated Hook
preservation, removal, legacy deduplication, and partial upload recovery against a
local OTLP receiver. Native desktop integration is not validated on this Linux host.
No LLM TTFT or absent token values are fabricated. Failed queues require another
terminal Hook to trigger retry; there is no always-running retry service.
