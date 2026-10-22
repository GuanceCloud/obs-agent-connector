# Codex sandbox observability

This is an **experimental connector-side runtime bridge** for [issue #34](https://github.com/GuanceCloud/obs-agent-connector/issues/34). It accepts explicit sandbox evidence, exports OTLP Logs with `source=agent_sandbox`, and associates sandboxes with successfully exported OBS Tool spans.

**Automatic stock Codex local sandbox collection is not implemented.** A producer that owns or observes the sandbox must supply the bridge events below. Enabling the bridge does not make the Stop Hook discover sandboxes. Guance inventory materialization and navigation UI are separate receiver/product work, outside this repository.

## Collection feasibility

Source inspection targets Codex `rust-v0.162.1`; the locally available CLI reports `codex-cli 0.162.1`. This is source inspection, not verification that a live deployment exports all events.

| Source | Available evidence | Limitation |
| --- | --- | --- |
| Existing transcript adapter | Session, turn, tool call, terminal turn, emitted OBS span identities | No authoritative sandbox identity or destruction event |
| Native exec-server telemetry | Process start, spawn failure, inferred sandbox denial, exit; launch context, conversation/tool ID, executor registration/environment, sandbox type | Does not establish sandbox instance ID, OS PID bridge, or isolation-resource destruction |
| This runtime bridge | Explicit producer identity, lifecycle evidence, tool association keys | Requires a separately integrated authoritative producer |

Sources: [process telemetry](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/exec-server/src/process_telemetry.rs), [sandbox preparation](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/exec-server/src/process_sandbox.rs), [process management](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/core/src/unified_exec/process_manager.rs).

A process exit, Tool completion, Session ID, or turn boundary is never substituted for sandbox identity or destruction. Local OS isolation and managed containers/VMs have different lifecycle evidence. This change installs no Codex fork, wrapper, native exporter rewrite, or additional Hook registration.

## Scope and configuration

Identity/state snapshots, lifecycle events, and trace associations use OTLP Logs. Existing Agent/Tool spans retain OTLP Traces and their actual OBS hierarchy. No full-lifetime sandbox span or duplicate inferred operation span is generated. Process/file/network auditing and resource Metrics are out of scope.

Exports use OTLP/HTTP Protobuf with gzip. Logs use the same `endpoint`, headers, and Basic Auth settings as the existing GTrace upload configuration. Configure a relative `logsPath` in `~/.obs-agent-connector/codex/gtrace.json`:

```json
{
  "enabled": true,
  "endpoint": "https://llm-openway.guance.com",
  "logsPath": "v1/write/otel-logs",
  "sandbox": { "enabled": false, "max_pending": 1024 }
}
```

Merge these settings into the existing file without replacing Trace/Metrics routes, credentials, or Agent identity. The GTrace profile uses `v1/write/otel-logs`; standard OTLP uses `v1/logs`. Normal installation and config edits seed the Logs path only when the existing Trace/Metrics paths match a recognized profile. Custom relative Logs paths are preserved; unknown profiles require an explicit path.

Starting with RC4, `otel_logs_url` and `--logs-url` are removed. The runtime ignores the retired key, and normal installation/config edits delete it. `update codex` continues preserving the runtime file byte-for-byte through `--no-config`, so an old ignored key can remain until an explicit configuration edit. Retired full-URL-only configurations require a valid `endpoint` and `logsPath` before Logs can be sent. Existing queues scoped to a former full-URL destination are not migrated to another destination.

For an existing GTrace installation:

```bash
obs-agent-connector config codex list
obs-agent-connector config codex edit --logs-path=v1/write/otel-logs
```

The edit preserves the sandbox enable state. Enable the bridge only after an independent authoritative producer is available:

```bash
obs-agent-connector config codex edit \
  --logs-path=v1/write/otel-logs \
  --sandbox-enabled=true \
  --sandbox-max-pending=1024
```

The same three sandbox flags are accepted by `install codex`. Explicit empty path edits are honored for that edit; later config edits can seed the recognized profile default again. Enabling requires a valid endpoint and effective relative Logs path. Invalid settings are rejected before configuration writes or installation changes. No other Agent receives Codex sandbox defaults.

These commands save configuration only. A separate producer still needs to feed `internal codex-sandbox`; neither configuration edits nor installation starts automatic Codex sandbox collection. `source=agent_sandbox` is fixed in the bridge and needs no source setting in `gtrace.json`.

Both enable flags must be true. When disabled, the bridge does not read input or create sandbox state. `max_pending` accepts 1 through 4,096, defaulting to 1,024 for missing/out-of-range values. `captureContent: "none"` suppresses free-text reasons; other modes redact and limit reasons to 1,024 characters. Names, types, and configured resource values are sanitized and bounded. Normal installer updates preserve these configuration keys without automatically enabling or starting the bridge.

## Runtime producer contract

Run a producer and the internal bridge independently of turn-scoped Hooks:

```text
runtime producer --NDJSON--> obs-agent-connector internal codex-sandbox
                                      |
                                durable journal
                                      |
                                  OTLP Logs
```

The producer must retain stable event IDs and isolate bridge unavailability or exit from the Agent. The bridge consumes one JSON object per line, at most 64 KiB per line. This is an internal integration entry point, not a public sandbox-management command.

This is a **synthetic contract example**, not a native Codex payload:

```json
{"schema_version":1,"event_id":"evt-001","event_name":"obs.sandbox.snapshot","timestamp":"2026-10-21T01:00:00Z","sandbox_id":"sbx-001","executor_id":"executor-001","identity_source":"native","runtime_source":"example-runtime","name":"example-sandbox","type":"container","state":"running","created_at":"2026-10-21T00:59:00Z","session_id":"session-001","turn_id":"turn-001","tool_call_id":"call-001","execution_id":"exec-001"}
```

Required fields are `schema_version=1`, `event_id`, `event_name`, actual occurrence `timestamp` in RFC 3339, `sandbox_id`, `executor_id`, `identity_source` (`native` or `observer`), and producer provenance `runtime_source`. An observer ID must represent an actual identified isolation instance with a documented boundary. Instance IDs must be workspace-unique, including executor namespace where necessary.

Optional fields are `name`, `type`, `state`, `created_at`, `destroyed_at`, `execution_id`, `reason`, and the association triple `session_id`, `turn_id`, `tool_call_id`. Supply all three association keys together or omit them all. Identity fields are bounded to 256 bytes. Unknown fields, including arbitrary native trace contexts and command/output payloads, are rejected.

| EventName | Meaning |
| --- | --- |
| `obs.sandbox.discovered` | Observation only; records first-seen time without manufacturing creation |
| `obs.sandbox.created` | Explicit successful creation evidence; event time is creation time |
| `obs.sandbox.ready` | Explicit readiness evidence |
| `obs.sandbox.snapshot` | Observed state and any known historical timestamps |
| `obs.sandbox.destroyed` | Explicit completed destruction evidence; event time is destruction time |
| `obs.sandbox.failed` | Sandbox lifecycle failure, not an arbitrary failed tool command |
| `obs.sandbox.observation.lost` | Observation unavailable; cannot establish destruction |
| `obs.sandbox.trace.bound` | Association-only request; OBS context is resolved locally |

States are `unknown`, `creating`, `created`, `ready`, `running`, `destroying`, `destroyed`, and `failed`. Lifecycle event/state contradictions and inconsistent historical timestamps are rejected. Unknown timestamps are omitted.

## OTLP data and trace binding

Stable Resource fields include sandbox/executor identity and sanitized configured resource attributes, with default `service.name=codex-sandbox-runtime`. LogRecords carry EventName, separate occurrence/observed timestamps, `source=agent_sandbox`, `obs.schema.version=1`, `obs.event.id`, sandbox/executor IDs, and provenance. Mutable state belongs in record attributes, while known lifecycle timestamps and optional reason use the structured Body.

Producer event IDs are scoped by executor/provenance and hashed into `obs.event.id`. Generated relations have stable derived IDs. Retries retain the same normalized record and clocks.

The terminal Hook pins OBS trace/span IDs before first upload. Only identities and association keys are persisted, not messages, arguments, or results. Successful Trace export confirms those IDs. Metrics retries reuse them without resending successful Traces.

The bridge recovers confirmed identities on startup and every 15 seconds. Events can precede their Trace; a separate association LogRecord is generated when the matching OBS Tool span is confirmed. One sandbox/execution can relate to multiple traces, and one trace can relate to multiple sandboxes. Association logs carry top-level TraceId/SpanId without repeating lifecycle state. Native Codex launch trace IDs are not substituted for OBS IDs. Completed Agent spans are never reopened. Legacy uploads lacking recoverable identity/scope metadata are skipped.

## Recovery and bounds

State lives under `<state_dir>/sandbox/<destination-scope>/`. Scope is a digest of destination/authentication settings; credentials are never stored in the sandbox journal. Changing either creates a separate queue and prevents applying old trace bindings to a new target. Old queues require the original configuration to drain; credential rotation does not automatically migrate records.

- Pending Logs are capped by `max_pending`; old unsent events are never evicted to admit new ones.
- Binding and relation tables are each capped at twice `max_pending`. Confirmed bindings/completed relations have a seven-day inactivity window; unresolved relations do not silently expire.
- Recent deduplication receipts retain approximately twice `max_pending`. Replays outside this window may be delivered again; receiver-side deduplication is required.
- The JSON journal is capped at 16 MiB. Updates use a short cross-process claim and atomic synced replacement. Crash-held claims expire after ten minutes. Network I/O runs outside the claim.
- Collection and delivery run independently. Each delivery pass sends at most 16 individual records. Timeouts are capped at ten seconds; EOF gets one bounded final pass. Remaining records survive restart.
- Network failures and HTTP 429/502/503/504 retain records with persisted exponential backoff and jitter (up to approximately 32 seconds), respecting longer `Retry-After` delays up to one day. The worker checks eligibility every second. Permanent HTTP failures or explicit record rejection remove the individual record and increment the persisted rejection counter. Malformed success responses remain queued. Warning-only responses are accepted. Server response bodies are not journaled.
- Single-record requests make OTLP partial rejection unambiguous; rejected records are not blindly replayed. Delivery remains at least once.
- Invalid input and journal errors are reported as bounded categories in the Hook diagnostic log, without payloads or credential-bearing URLs. Capacity exhaustion also increments a persisted dropped counter. Producers must replay unaccepted input with the same IDs.

To attempt one bounded recovery pass without reading events:

```bash
obs-agent-connector internal codex-sandbox --flush
```

The bridge returns nonzero on input/export failure; its producer must isolate this exit from Codex. The terminal Hook remains fail-open by default. Sandbox association errors can be recovered from confirmed identity files without changing Agent Trace status.

## Local delivery diagnostics

`gtrace.json` contains configuration only. Each Logs delivery attempt records its outcome in `~/.obs-agent-connector/codex/gtrace-hooks.json`, alongside existing Trace/Metrics diagnostics:

- `uploaded logs`: successful OTLP response, including HTTP status, `signal=logs`, `source=agent_sandbox`, record count, and rejected-record count.
- `logs upload failed`: delivery failure with HTTP status (0 when unavailable), bounded error category, retryability, and rejected-record count. HTTP 200 with OTLP partial rejection is a failure, not a successful upload.
- `logs retry scheduled`: persisted next retry time in UTC and retry delay in milliseconds.

No successful delivery is recorded for an empty queue or a retry that is not yet due. Entries exclude payloads, sandbox reasons, destination URLs, authentication, and server response bodies. Diagnostic write failures do not block delivery or change its outcome. Existing Hook-level export warnings can also describe a failed pass. These local diagnostics are not exported as sandbox lifecycle Logs.

## Receiver/product acceptance still required

Verify the target route preserves EventName, both clocks, custom attributes, structured Body, and trace context. Map the LogRecord attribute `source` to the Guance log source and confirm records are queryable as `source=agent_sandbox`. OTLP request acceptance alone does not verify this mapping.

Build the workspace-scoped inventory from snapshots/lifecycle records; preserve immutable history, deduplicate events, and handle out-of-order delivery without regressing terminal state. Observation loss must not overwrite confirmed destruction. Trace relations must support either side arriving first and navigation in both directions.

Local tests cover protocol fields, a test receiver, concurrency, cross-turn/delayed associations, restart/replay, capacity, partial rejection, and Trace/Metrics retries. They do not prove an authoritative stock Codex producer or live Guance inventory support. These remain acceptance gates for completing issue #34.
