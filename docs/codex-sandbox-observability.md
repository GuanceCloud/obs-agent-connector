# Codex sandbox observability

This is an **experimental connector-side runtime bridge** for [issue #34](https://github.com/GuanceCloud/obs-agent-connector/issues/34). It accepts explicit sandbox evidence, exports OTLP Logs with `source=agent_sandbox`, and associates sandboxes with successfully exported OBS Tool spans.

**Automatic Linux Codex namespace observation is implemented.** Installed native lifecycle Hooks start a managed observer when both enable flags are true. It observes actual Codex PID-namespace sandbox instances, not permission settings or ordinary tools. Other operating systems, legacy Landlock-only isolation, and remote sandboxes still require an independent authoritative producer. Guance inventory materialization and navigation UI remain outside this repository.

## Collection feasibility

Source inspection and the opt-in real-helper integration test target Codex `rust-v0.163.0` / `codex-cli 0.163.0`. The integration test executes the real Linux sandbox helper without model/API credentials and verifies automatic OTLP Logs and association to a normally exported OBS Tool span.

| Source | Available evidence | Limitation |
| --- | --- | --- |
| Existing transcript adapter | Session, turn, tool call, terminal turn, emitted OBS span identities | No authoritative sandbox identity or destruction event |
| Native exec-server telemetry | Process start, spawn failure, inferred sandbox denial, exit; launch context, conversation/tool ID, executor registration/environment, sandbox type | Does not establish sandbox instance ID, OS PID bridge, or isolation-resource destruction |
| Native Linux observer | Kernel namespace-init identity, actual security restrictions, observed lifecycle, exact Hook command/session match | Polling can miss very short executions; exact creation/destruction clocks are unavailable |
| External runtime bridge | Explicit producer identity, lifecycle evidence, tool association keys | Requires a separately integrated authoritative producer |

Sources: [process telemetry](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/exec-server/src/process_telemetry.rs), [sandbox preparation](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/exec-server/src/process_sandbox.rs), [process management](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/core/src/unified_exec/process_manager.rs).

A process exit, Tool completion, Session ID, or turn boundary is never substituted for sandbox identity or destruction. Local OS isolation and managed containers/VMs have different lifecycle evidence. This integration installs no Codex fork, command wrapper, or native exporter rewrite. It registers native lifecycle Hooks with the existing connector command.

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

The edit preserves the sandbox enable state. Enable native Linux observation and the optional bridge:

```bash
obs-agent-connector config codex edit \
  --logs-path=v1/write/otel-logs \
  --sandbox-enabled=true \
  --sandbox-max-pending=1024
```

The same three sandbox flags are accepted by `install codex`. Explicit empty path edits are honored for that edit; later config edits can seed the recognized profile default again. Enabling requires a valid endpoint and effective relative Logs path. Invalid settings are rejected before configuration writes or installation changes. No other Agent receives Codex sandbox defaults.

Configuration commands save settings. Run `obs-agent-connector update codex` once to install the new lifecycle registrations while preserving the runtime configuration. The next SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Stop, or Interrupt Hook starts or recovers the observer. `source=agent_sandbox` is fixed and needs no source setting in `gtrace.json`.

Both enable flags must be true. When disabled, the bridge does not read input or create sandbox state. `max_pending` accepts 1 through 4,096, defaulting to 1,024 for missing/out-of-range values. `captureContent: "none"` suppresses free-text reasons; other modes redact and limit reasons to 1,024 characters. Names, types, and configured resource values are sanitized and bounded. Normal installer updates preserve these configuration keys and never automatically enable observation. Enabled lifecycle Hooks start the observer as needed.


## Native Linux collection

The boundary is Codex's actual isolated PID 1 supervisor (`argv[0]=codex-linux-sandbox`), running the Codex executable in a separate mount and PID namespace with `no_new_privs=1` and seccomp filtering active. Its Codex ancestor is verified. Collection is restricted to the same OS user and effective `CODEX_HOME`; separate installations and private acceptance runs are not mixed. A command, session, configured permission mode, arbitrary child PID, or generic container is insufficient evidence.

The observer derives a stable **observer identity** from the kernel boot ID, namespace-init PID/start ticks, and mount/PID/user namespace identities. Executor identity uses the actual Codex ancestor PID/start ticks and boot ID. PID reuse creates a different instance; multiple namespace children do not create extra inventory resources. Provenance is `obs.collection.source=codex.native.linux.proc`, `obs.sandbox.identity_source=observer`, type `linux-pid-namespace`.

- First sight emits `obs.sandbox.discovered`, state `running`; continuing observations produce at most one running snapshot per minute.
- Confirmed exit/disappearance of this previously observed **namespace-init**, including PID reuse, produces a terminal **snapshot** with state `destroyed`. A PID-namespace init is the supervising boundary, rather than an arbitrary Tool child process. Unknown exact creation/destruction dates remain omitted. Detection time is the snapshot occurrence time, not a fabricated destruction timestamp.
- Permission/read failures emit `obs.sandbox.observation.lost`, state `unknown`; they do not imply destruction. Recovery restores running observation.
- Native Hooks persist session/turn/call IDs and a command digest. The observer reads only allowlisted `CODEX_THREAD_ID` and `CODEX_TOOL_CALL_ID` metadata from the verified namespace-init or its direct command child. Child context requires matching UID, parent/start identity, all namespaces and exact command digest; conflicting contexts remain unlinked. Late command context enriches the original sandbox identity and can complete association after exit. Commands, results and environment contents are never persisted.
- Prefer the exact native session and Tool call ID within the matching Hook window, then use that Hook's turn ID to resolve `(session, turn, call)` to successfully exported OBS Span identities. This is marked `obs.sandbox.association.method=native_process_tool_id` with `confidence=observed`. Each binding uses the actual uploaded Trace ID and Tool Span ID; Hooks do not independently create or implicitly propagate Traces. Missing native Tool IDs may fall back to a unique session/command digest with method `native_thread_command_digest` and `confidence=inferred`. A present but unmatched native Tool ID never falls back to command guessing. Concurrent identical calls remain distinguishable when exact native IDs exist; ambiguous matches remain unlinked.
- A known native session is preserved in `gen_ai.conversation.id` without inventing a turn/Tool link. Missing OBS Tool identity remains pending; native launch trace IDs are never substituted. Missing PreToolUse/PostToolUse registrations prevent collecting the required turn context: run `update codex` with the new connector to reconcile registrations. Stop-only installations can continue lifecycle collection but do not establish this association path. Missed historical sandbox context cannot be reconstructed from timestamp proximity.
- Hooks perform no network I/O for collection startup. A detached singleton uses a kernel file lock (released on crash) and a bounded readiness wait. Scanning and Logs delivery run independently. Configuration disable/destination changes or replacement of the connector executable stop it; later Hooks restart it. It exits after 30 minutes without activity.
- Polling is 20 ms shortly after Hook activity and 100 ms otherwise. Commands that live entirely between scans may be missed. This is best-effort observation, not a complete kernel execution audit. Full-access execution correctly creates no sandbox instance. Non-Linux and Landlock-only modes are not automatically observed.
- A private producer outbox preserves stable events across a crash between observer state and the delivery journal. Native state is bounded to 1,024 tool contexts, 1,024 instance entries, 1,024 producer events, and 4 MiB. Old terminal instance metadata can be evicted; live instances and undelivered events are not silently displaced.

Linux implementation evidence: [native Hook input](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/hooks/src/events/pre_tool_use.rs), [namespace supervision and sandbox enforcement](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/linux-sandbox/src/linux_run_main.rs). Existing Codex processes may need a new turn/session for updated Hook registrations to take effect; the collector does not reconstruct missed historical sandbox instances.

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

Optional fields are `name`, `type`, `state`, `created_at`, `destroyed_at`, `execution_id`, `reason`, `association_method`, `observed_session_id`, and the association triple `session_id`, `turn_id`, `tool_call_id`. Supply all three association keys together or omit them all. `observed_session_id` carries independently observed session context without requiring or inventing a turn/tool link; when both are supplied, the sessions must agree. Association method is `runtime_keys` (default for supplied keys), `native_process_tool_id` (observed native process metadata), or `native_thread_command_digest` (inferred command correlation). Identity fields are bounded to 256 bytes. Unknown fields, including arbitrary native trace contexts and command/output payloads, are rejected.

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

The fixed source is sent as **Resource `log.source=agent_sandbox`**, while `service.name` retains the configured service identity (default `codex-sandbox-runtime`). The record also carries `source=agent_sandbox`. Resource `log.source` is reserved by the bridge and cannot be overridden by configured tags. No additional source option is needed. Guance's OTLP receiver selects Resource `log.source` before `service.name`; a record-only `source` attribute does not select the log source.

Every record carries the native OTLP `EventName` **and** a matching `event_name` attribute. This preserves a queryable event type on receivers that do not extract the newer native field. `obs.event.id` is a deduplication identity, not an event type.

The Body is a **UTF-8 JSON string in OTLP `AnyValue.string_value`**, not a KVList or a JSON-encoded protobuf object. It provides a readable summary. Fields needed for inventory and association remain independently queryable attributes; consumers must not depend on parsing `message` to obtain them.

| Meaning | OTLP location | Guance stored field |
| --- | --- | --- |
| Log source | Resource `log.source`, fixed `agent_sandbox` | `source` |
| Service identity | Resource `service.name` | `service` |
| Agent identity | Resource `agent_id`, `agent_name` when configured | `agent_id`, `agent_name` |
| Contract version | Attribute `obs.schema.version=1` | `obs_schema_version` |
| Event type | Native EventName and attribute `event_name` | `event_name` |
| Scoped event identity | Attribute `obs.event.id` | `obs_event_id` |
| Sandbox identity | Attribute `obs.sandbox.id` and Resource copy | `obs_sandbox_id` |
| Executor identity | Attribute `obs.executor.id` and Resource copy | `obs_executor_id` |
| Optional name/type | Attributes `obs.sandbox.name`, `obs.sandbox.type` | `obs_sandbox_name`, `obs_sandbox_type` |
| Optional observed state | Attribute `obs.sandbox.state` | `obs_sandbox_state` |
| Known creation/destruction | Attributes `sandbox_created_at`, `sandbox_destroyed_at` | Same names |
| Identity provenance | Attribute `obs.sandbox.identity_source` | `obs_sandbox_identity_source` |
| Collector provenance | Attribute `obs.collection.source` | `obs_collection_source` |
| Optional execution identity | Attribute `obs.execution.id` | `obs_execution_id` |
| Association lookup keys | Attributes `gen_ai.conversation.id`, `run_id`, `gen_ai.tool.call.id` | `gen_ai_conversation_id`, `run_id`, `gen_ai_tool_call_id` |
| Association evidence | Attributes `obs.sandbox.association.method`, optional `obs.sandbox.association.confidence` | `obs_sandbox_association_method`, `obs_sandbox_association_confidence` |
| Confirmed OBS trace context | Native TraceId and SpanId on the association record | `trace_id`, `span_id` |
| Event occurrence time | Native TimeUnixNano | Native log timestamp |
| Bridge observation time | Native ObservedTimeUnixNano | Receiver-dependent; also in lifecycle Body `last_observed_at` |
| Readable summary | Body string | `message` |

Dots in custom attribute names are normalized to underscores by the Guance receiver. Other OTLP receivers may preserve dotted names. Lifecycle timestamps are UTC RFC 3339 strings with available fractional-second precision; unknown times are omitted, never inferred from ingestion time. Producer input fields remain `created_at` and `destroyed_at`. The event timestamp means occurrence time; `created` and `destroyed` establish the corresponding time, while `ready` and `snapshot` do not invent creation. Body `first_seen_at` on discovery means the bridge's first observation of that discovery event, not sandbox creation or a global first-ever sighting.

A creation Body example (illustrative identities):

```json
{"event_name":"obs.sandbox.created","sandbox_id":"sbx-001","executor_id":"executor-001","state":"created","sandbox_created_at":"2026-10-23T04:00:00Z","last_observed_at":"2026-10-23T04:00:00.1Z"}
```

An association Body contains only event type and sandbox/executor identity:

```json
{"event_name":"obs.sandbox.trace.bound","sandbox_id":"sbx-001","executor_id":"executor-001"}
```

Association records carry lookup keys and confirmed native trace/span IDs outside the Body. They never repeat lifecycle state, lifecycle dates, or reasons. The optional sanitized reason appears only in lifecycle Body when content capture permits it.

Starting with RC6, pending records from earlier releases acquire the same source, event-type, and string-Body contract at export. Legacy Body-only lifecycle dates become prefixed queryable attributes. Event IDs and both native clocks are preserved. Already ingested records are not rewritten; query new records to validate the corrected source and schema.
Producer event IDs are scoped by executor/provenance and hashed into `obs.event.id`. Generated relations have stable derived IDs. Retries retain the same normalized record and clocks.

The terminal Hook pins OBS trace/span IDs before first upload. Only identities and association keys are persisted, not messages, arguments, or results. Successful Trace export confirms those IDs. Metrics retries reuse them without resending successful Traces.

The bridge recovers confirmed identities on startup and every 15 seconds. Events can precede their Trace; a separate association LogRecord is generated when the matching OBS Tool span is confirmed. One sandbox/execution can relate to multiple traces, and one trace can relate to multiple sandboxes. Association logs carry top-level TraceId/SpanId without repeating lifecycle state. Native Codex launch trace IDs are not substituted for OBS IDs. Completed Agent spans are never reopened. Legacy uploads lacking recoverable identity/scope metadata are skipped.

## Recovery and bounds

State lives under `<state_dir>/sandbox/<destination-scope>/`. Scope is a digest of destination/authentication settings; credentials are never stored in the sandbox journal. Changing either creates a separate queue and prevents applying old trace bindings to a new target. Old queues require the original configuration to drain; credential rotation does not automatically migrate records.

- Pending Logs are capped by `max_pending`; old unsent events are never evicted to admit new ones.
- Binding and relation tables are each capped at twice `max_pending`. The confirmed binding warm cache evicts the oldest recoverable entries when full, while protecting unresolved associations. Recovery loads only identities requested by unresolved relations from confirmed upload files. Completed relations have a seven-day inactivity window; unresolved relations do not silently expire.
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

## Data-side acceptance and remaining integration

Data acceptance must confirm the actual target route stores new records under `source=agent_sandbox`, with a readable JSON `message`, queryable `event_name`, prefixed lifecycle dates, identity/provenance attributes, and actual trace context on resolved associations. HTTP success alone does not establish receiver compatibility.

The compatibility rules above follow [DataKit source selection](https://github.com/GuanceCloud/datakit/blob/master/internal/plugins/inputs/opentelemetry/logs.go) and [the OTLP Logs parser](https://github.com/GuanceCloud/cliutils/blob/master/otlp/logs_parse.go). The receiver renders a string Body directly, while a KVList can become protobuf diagnostic text. Contract tests model source precedence and verify decoded outgoing protobuf; deployment acceptance must additionally inspect stored records.

Local tests cover all supported event types, privacy, receiver fields, kernel evidence, PID reuse, command ambiguity, native producer outbox recovery, cache rotation, legacy queued records, concurrency, cross-turn/delayed associations, restart/replay, partial rejection, and Trace/Metrics retries. The opt-in integration test executes the actual Codex Linux sandbox and the connector CLI against a protobuf receiver, including Hook startup and association to the normally exported Tool span. Other isolation/runtime types and exhaustive short-process capture remain outside this native implementation.

Platform inventory, deduplication/materialization, out-of-order state projection, and bidirectional navigation are deferred. This document defines the input and exported data contract for that later design; no platform implementation is included here.

To run the real-helper acceptance test after building the connector:

```bash
go build -o /tmp/obs-native-connector ./cmd/obs-agent-connector
OBS_CONNECTOR_TEST_BINARY=/tmp/obs-native-connector \
OBS_CODEX_TEST_BINARY=/absolute/path/to/codex \
go test ./internal/adapters/codex/sandbox -run TestNativeCodexSandboxCLIEndToEnd -v -count=1
```

It uses a private temporary HOME, a local OTLP receiver, the native sandbox helper, and a marked transcript fixture. It requires no model call or real upload credentials.

## Real-host association acceptance

The opt-in `TestNativeCodexRealHostAssociation` runs two concurrent Codex CLI sessions against a local deterministic model fixture and OTLP receiver. Codex assigns real session/turn identifiers, executes identical commands with the same fixture call ID in separate sessions, triggers installed/trusted Hooks and writes its own transcripts. The test verifies that two sandbox association logs reference their respective actually exported OBS Tool spans and distinct Trace IDs. No Hook payload, native environment identity, sandbox event or transcript is injected. No model credentials are used. This supplements the helper-only fixture and does not claim exhaustive desktop/remote or kernel-audit coverage.
