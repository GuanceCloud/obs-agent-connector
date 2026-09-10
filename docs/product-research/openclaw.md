# Built-in OpenClaw Adapter

## Architecture and source evidence

The connector embeds `internal/adapters/openclaw/bridge/index.mjs` in the Go binary.
The installer extracts this dependency-free native entry point under
`~/.obs-agent-connector/openclaw/plugin/` and adds that directory to
`plugins.load.paths` in `openclaw.json`. OpenClaw loads it using its own Node.js
runtime; no external plugin download, npm installation, or telemetry SDK is needed.

The bridge observes `llm_input`, `llm_output`, `model_call_started`,
`model_call_ended`, `before_tool_call`, `after_tool_call`, and `agent_end` using
`api.on`. Only `agent_end` submits a terminal payload to
`obs-agent-connector hook openclaw`, using argument arrays without a shell.

Sources reviewed on 2026-09-10:

- [Native plugin hooks and conversation access](https://docs.openclaw.ai/plugins/hooks)
- [Prompt/session hooks and harness boundaries](https://docs.openclaw.ai/plugins/hooks/prompt-and-session)
- [Native event type definitions](https://github.com/openclaw/openclaw/blob/main/src/plugins/hook-types.ts)
- Local `openclaw-otel-plugin` configuration and snapshot-replay implementation.

`agent_end` supplies success, optional error and duration, and final messages.
Run IDs and session identity are optional in some host paths. The adapter skips
an event without both stable identifiers instead of joining unrelated runs.
The host may gate conversation hooks; installation sets
`plugins.entries.obs-agent-connector.hooks.allowConversationAccess=true`.

## Collection behavior

- Terminal snapshots are restricted to the current user boundary and run window.
  Old messages are not replayed as a new request.
- Per-assistant-message usage is preferred. `llm_output.usage` may summarize an
  entire attempt and is never assigned to a single LLM span. When the snapshot
  is unavailable, the bridge can recover `lastAssistant` and its own usage.
- Tools use native IDs, results, errors, and observed boundaries when emitted.
  Transcript `toolCall` / `toolResult` records provide a fallback. Explicit reads of a
  `SKILL.md` file can produce a child skill span.
- Root, LLM, tool, skill, and assistant spans use the shared semantic builder;
  the four standard metrics are derived from those same spans. Native first-chunk
  observations remain Trace attributes and do not add another default metric.
- Heartbeat, cron, system, internal, and title triggers are skipped when the host
  identifies them. Text is not used to invent subagent relationships.
- Content modes are `none`, `preview`, and `full`. Captured content and errors
  pass through shared recursive redaction before being written to the retry spool.

## Configuration and lifecycle

```bash
obs-agent-connector install openclaw --endpoint https://example.com --x-token TOKEN
obs-agent-connector update openclaw
obs-agent-connector config openclaw
obs-agent-connector disable openclaw
obs-agent-connector enable openclaw
obs-agent-connector remove openclaw
```

The telemetry configuration lives in
`~/.obs-agent-connector/openclaw/gtrace.json`. Registration defaults to
`~/.openclaw/openclaw.json`; `OPENCLAW_STATE_DIR` and `OPENCLAW_CONFIG_PATH`
override the host registration location. The installer currently requires strict
JSON and returns an error without overwriting JSON5/commented host configuration.

Runtime precedence is defaults, legacy `~/.openclaw/gtrace.json`, managed config,
then `OPENCLAW_OTEL_*`, standard OTLP, and supported `GTRACE_*` environment values.
The bridge passes its exact config path as `OPENCLAW_OTEL_CONFIG_FILE` to Go.
The managed enable switch is reread for every observed event.

Installation copies compatible transport, privacy, and identity settings from
`plugins.entries.openclaw-otel-plugin.config` when no managed config exists.
Explicit install options override the corresponding fields. Updating uses
`--no-config`: an existing managed config remains byte-for-byte unchanged;
first-time migration can create a managed copy of legacy settings.
The old plugin entry is disabled but its config and files remain. An explicitly
configured `diagnostics-otel` entry is also disabled to avoid duplicate reporting.
Unrelated registration entries, load paths, and allowlist entries are retained.

After a successful installation or update, the connector attempts `openclaw gateway restart`
to unload the previous plugin and load the embedded bridge. The restart has a
20-second timeout. Failure does not roll back or fail installation; a warning
provides the manual restart command.

`remove openclaw` unregisters the bridge and deletes the connector-managed
OpenClaw directory, removes legacy plugin directories under `extensions/` and
`plugins/`, and disables the legacy plugin entry while preserving its settings.
Legacy load paths, allowlist entries, and installation records are also removed.
Legacy-only installations can be removed even though `list` only reports the
built-in installation. `--purge-config` also removes the legacy plugin's nested config.
Old telemetry plugins are not automatically re-enabled. `uninstall --keep-config`
removes the bridge and upload state while retaining managed telemetry settings.

## Reliability and limits

Go stores sanitized terminal turns in `state/pending/`, claims each
`(session ID, run ID)`, and records successful signals independently. Trace success
followed by Metrics failure retries only Metrics. The bridge retries pending work
on service startup, shutdown, every minute, and subsequent terminal events.
Disabling telemetry stops input processing and retry uploads.

The bridge bounds each subprocess to 25 seconds, allows at most four concurrent
subprocesses, and limits a payload to 8 MiB. In-memory observations are bounded to
64 runs, 256 observations and 1 MiB of selected event data per run, with a one-hour
TTL. Over-limit observations or submissions are dropped to protect the host.
Terminal payloads not yet handed to Go do not survive a Gateway crash.

This implementation does not claim parity with the external plugin's diagnostic
stream, historical trajectory sweeps, logs export, or cross-run subagent linking.
Harnesses expose different hooks; missing terminal events cannot be reconstructed.
Native model-call completion events provide call IDs and durations. When present,
they define the LLM spans and replace transcript-derived LLM spans, avoiding double
counting. Transcript token usage is retained at the root only and does not produce
`gen_ai.client.token.usage`, because that metric is derived only from attributable
`llm` usage. Per-call token/model breakdown is unavailable
when transcript messages cannot be reliably joined to native calls. Transcript-only
tool-to-LLM links are omitted on this path.

When native model-call events are absent, unambiguous paired `llm_input` and
`llm_output` events provide observed Hook boundaries
(`openclaw.timing_source=native_hook_boundary`). Terminal snapshot messages
supplement output, token usage, and tool results, but do not define model timing.
Calls with no reliable timing are omitted instead of emitting a fabricated 1 ms
duration; token usage remains a turn aggregate and
`openclaw.llm_timing_unavailable=true` marks the missing timing. Assistant output
uses the observed final model/output completion through `agent_end`, following
the legacy collector's output-finalization phase (`model_end_to_run_end`). This
is runtime finalization, not measured client delivery latency. Without that
boundary, it is a zero-duration terminal event. No minimum visible duration is added. Both paths retain
`trace_completeness=partial`; missing call/content correlation is not reconstructed.
Tool durations use paired `before_tool_call` and `after_tool_call` boundaries, or
the host-reported positive duration. Calls without either form of timing are omitted.

## Validation

Automated checks cover native bridge registration and argv, interleaved runs,
disabled and spawn-failure paths, terminal parsing, stale snapshots, per-message
usage, tool/skill association, cancellation, privacy, duplicate terminal events,
OTLP Protobuf decoding, partial signal failure and process restart, concurrent
claims, install/update/remove, custom host roots, and malformed configuration.
Node.js bridge tests are included in `go test ./...` when Node.js is available.

A temporary-HOME end-to-end check also exercised the built executable installer,
extracted JavaScript bridge, real Go subprocess, local GTrace Trace/Metrics
receiver, and removal without touching user configuration.

A real OpenClaw Gateway conversation and native Windows/macOS execution remain
manual acceptance checks; cross-compilation alone does not prove host compatibility.

## First response chunk latency

When the host emits `model_call_ended.timeToFirstByteMs`, the adapter records the
timing on the corresponding LLM span. This is not strict TTFT and is not
user-message-to-first-visible-text latency: the first observed response chunk may
be empty or contain metadata.

Each native LLM span carries the numeric standard attribute
`gen_ai.response.time_to_first_chunk` in seconds (for example, `0.125` means
125 ms). The default metric set does not include a first-chunk histogram.
`openclaw.model_call_id` and `openclaw.first_chunk.source` retain call identity and
source evidence on the span. The former root `model.first_chunks` JSON array is
no longer emitted. A `ttft` alias is not added.

Calls are deduplicated by native call ID within a run. Missing, nonnumeric,
negative, nonfinite, or greater-than-duration latency values are omitted while
valid native call durations remain observable. A measured zero is retained.
Calls failing after the first chunk receive span error status and `error.type`.
No latency or streaming-mode flag is inferred from transcript timing.

The connector follows the [GTrace AI semantic conventions](https://github.com/GuanceCloud/guance-gtrace-ai-semantic-conventions),
which define the supported trace tree, attributes, and derived metric set.
The server-side `gen_ai.server.time_to_first_token` metric is outside this client
adapter's scope.

The observations are persisted with the terminal turn and follow the same
independent Trace/Metrics retry path. Older hosts and harnesses that omit the
native timing event produce no samples. Update the adapter and restart the
OpenClaw Gateway to load the new bridge.

## Hook diagnostics

OpenClaw writes structured JSONL to `~/.obs-agent-connector/openclaw/gtrace-hooks.json`,
using the shared `ts`, `message`, and optional `extra` envelope. Standard lifecycle
messages are `hook invoked`, `parsed transcript`, `uploaded spans`, and
`uploaded metrics`. Upload records include HTTP status and item counts; successful
signals are not logged again when a duplicate event or retry skips their upload.

Additional diagnostics include `hook failed`, `turn skipped`, `upload failed`, and
`bridge failed`. Bridge failures report a fixed reason for spawn, stdin, child exit,
or timeout errors, without recording raw errors or conversation payloads. Logging
failures do not fail the host conversation. Periodic flushes may upload pending
signals without a new transcript parsing record.

## GenAI message content

When `captureContent` is `preview` or `full`, the root `invoke_agent` span records
`gen_ai.input.messages` and `gen_ai.output.messages`, and the `assistant` span
records `gen_ai.output.messages`. Values use the GenAI `role` and `parts` schema;
text parts are recursively sanitized and limited by `maxChars`. The `none` mode
omits message and preview content.

LLM spans also retain available `gen_ai.system_instructions`,
`gen_ai.tool.definitions`, provider/model fields, finish reasons, lengths, and
`output_kind`. Reasoning and tool calls remain structured message parts; tool
results use `role=tool` with a `tool_call_response` part.

An LLM span created from paired `llm_input` and `llm_output` hooks records both
standard message attributes. Native `model_call_ended` deliberately contains no
content or prompt identifiers. Its span receives content only when provider,
model, and containment within one input/output Hook window produce a unique
match. Ambiguous multi-call windows retain content at the turn level and omit it
from individual LLM spans instead of duplicating or guessing attribution.
