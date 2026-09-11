# OMP GTrace Integration

OMP (Oh My Pi) is supported by a built-in Go collector and a small JavaScript
extension bundled into the connector binary. No npm install or separate plugin
repository is required for telemetry.

## Supported Product and Evidence

The extension contract was checked against **OMP 18.1.15**, tag commit
`a33cc26824e3c91edd9fa42d681f10dceb4ac2f0`. The installer requires 18.1.15 or later.
Later releases must retain the extension event contract; they have not all been
product-tested. macOS ARM64 was exercised with the real OMP binary and a local
synthetic OpenAI-compatible model. Linux and Windows are build targets; native
product execution on those platforms has not been verified.

Primary sources:

- [Extension API and events](https://github.com/can1357/oh-my-pi/blob/v18.1.15/docs/extensions.md)
- [Extension discovery and profiles](https://github.com/can1357/oh-my-pi/blob/v18.1.15/docs/extension-loading.md)
- [Agent lifecycle types](https://github.com/can1357/oh-my-pi/blob/v18.1.15/packages/coding-agent/src/extensibility/shared-events.ts)
- [Message and tool event types](https://github.com/can1357/oh-my-pi/blob/v18.1.15/packages/coding-agent/src/extensibility/extensions/types.ts)
- [Assistant message schema](https://github.com/can1357/oh-my-pi/blob/v18.1.15/packages/ai/src/types.ts)
- [Subagent event publication](https://github.com/can1357/oh-my-pi/blob/v18.1.15/packages/coding-agent/src/utils/event-bus.ts)
- [Subagent lifecycle and event payloads](https://github.com/can1357/oh-my-pi/blob/v18.1.15/packages/coding-agent/src/task/types.ts)
- [Native token normalization](https://github.com/can1357/oh-my-pi/blob/v18.1.15/packages/agent/src/telemetry.ts)

## Install and Manage

```bash
obs-agent-connector install omp
obs-agent-connector status omp
obs-agent-connector config omp list
obs-agent-connector disable omp
obs-agent-connector enable omp
obs-agent-connector update omp
obs-agent-connector remove omp
```

Installation reuses the connector endpoint and X-Token defaults. Fully exit
and restart the OMP process after installation or update. In OMP 18.1.15,
`/reload-plugins` refreshes skills, commands, agent discovery, and MCP; it does
not reload this JavaScript extension. Starting a new conversation in the same
process is insufficient. See the [reload implementation](https://github.com/can1357/oh-my-pi/blob/v18.1.15/packages/coding-agent/src/slash-commands/builtin-marketplace.ts).
The installer manages only `~/.omp/agent/extensions/obs-agent-connector.js` and
`~/.obs-agent-connector/omp/`. Other extensions and OMP settings are preserved.
An existing unrecognized file at the extension path is not overwritten.

`update omp` refreshes the extension while preserving `gtrace.json` byte for byte.
`remove omp` removes the extension and connector-managed OMP configuration, logs,
and state. Restart the OMP process after removing the extension. `uninstall --keep-config`
retains runtime configuration but removes the extension, logs, and state.

The installer honors `PI_CODING_AGENT_DIR` for OMP extension discovery. To target
a named profile, set this variable to that profile's agent directory for install,
status, update, and remove. For example:

```bash
PI_CODING_AGENT_DIR="$HOME/.omp/profiles/work/agent" obs-agent-connector install omp
```

All profiles use the same connector-managed OMP runtime config. Automatic
enumeration and removal across multiple profiles is not implemented. Use the
same profile override when removing an installation.

## Collection Flow

```text
agent_start / turn_start / context / message_end / tool_execution_*
  -> bounded, sanitized in-memory events
agent_end (willContinue is false)
  -> atomically write a temporary terminal snapshot
  -> detached obs-agent-connector hook omp --config <config> --snapshot <file>
  -> normalize one user-request Turn
  -> shared semantic Builder -> Span-derived Metrics -> OTLP/HTTP Protobuf
  -> record traces uploaded, metrics uploaded, then turn completed
```

OMP's `turn_start` describes an individual model/tool iteration. It does not
create a new GTrace root. Automatic retry/continuation events with
`agent_end.willContinue=true` retain the current request until the final end.
Image-only user requests retain telemetry without persisting image bytes.
Synthetic-only or agent-attributed requests are excluded. A UUID identifies the
captured request, and OMP supplies the session ID. In-process upload retries retain that UUID.

The extension observes events without returning control decisions or changing
provider context. Go uploads run in a detached process. Callback and worker
failures do not fail the OMP request.

## Trace and Metrics

- One `invoke_agent` root per captured terminal user request.
- `llm` spans use assistant message timestamps/duration and per-message usage.
- `tool:*` spans use observed tool execution start/end events and tool call IDs.
- `assistant` spans represent observed completed text output and carry no tokens.
  They remain present when content capture is disabled.
- `skill:*` is emitted only for a `read` call targeting an explicit `SKILL.md`.
- OMP input tokens are `input + cacheRead + cacheWrite`; output is `output`.
  Cache components also remain separate trace attributes.
- Input and output messages use GenAI message arrays with `role` and `parts`,
  including `text`, `reasoning`, `tool_call`, and `tool_call_response` parts.
  They are exported as structured OTLP attributes. Plain text belongs in preview
  fields. The OMP exporter excludes the shared core's legacy `gtrace.observation.*`,
  `gtrace.usage`, and `gtrace.model.name` aliases.
- Request context is captured from the extension `context` event, bounded to
  the most recent 100 messages. It may precede transformations by other extensions.
- The shared core produces workflow duration, operation count, operation duration,
  and input/output token usage. Session/run IDs are removed from metric dimensions.

Root model/provider and finish-reason fields summarize the last model call;
individual LLM spans retain each call's model. Text output spans carry
`gen_ai.output.type=text`. LLM `output_kind` distinguishes text and tool calls.
Input/output lengths count Unicode text characters before redaction and clipping,
including when content capture is disabled. LLM input length sums text in the
captured context (at most 100 messages); it does not count image bytes or tool
argument JSON. When an original length is unavailable, the available text length is used.
Workflow metrics preserve the root `final_status`, separately from aggregate
`status`, so cancelled requests remain distinguishable from errors.

Skill identity is extracted from explicit `read` calls targeting `SKILL.md`
before content redaction. Disabling content capture preserves the Skill name,
source classification, spans, and operation metrics; it omits paths and content.
Sources are `user` for the OMP user/profile directory and `workspace` for the
current project; unclassified sources are omitted. Description/version are
extracted only from complete, simple scalar frontmatter in captured read results.
Unsupported or truncated metadata is omitted; the collector does not open
additional files. Successful Skill results use `completed`.

## Subagent Traces

The extension subscribes to `task:subagent:lifecycle` and `task:subagent:event`
through `pi.events`. OMP publishes these frames to both its session event bus
and its root observability bus; a separate observability-bus API is not needed.
A lifecycle start must name an observed `parentToolCallId`. The child session ID
is read from the native session header (which may follow a title record); the
collector does not scan or replay transcript messages.

Each child run receives its own turn ID and terminal snapshot. Its root shares
its parent's Trace ID and points to the actual delegate tool span:

```text
invoke_agent (parent)
└── tool:task
    └── invoke_agent (child)
        ├── llm
        └── tool:task
            └── invoke_agent (nested child)
```

Parent roots and tool span IDs are stable across worker invocations; child span
IDs are distinct. Synchronous, parallel, nested, and detached child runs use the
same association mechanism. Child snapshots are uploaded on native lifecycle
completion/failure/cancellation, independently of when the parent completes.
Each snapshot retains separate Trace/Metrics upload checkpoints. Child tokens
come only from its own model events; task-result aggregate usage is not added
again to the parent. Standard span parent IDs express the relationship without
adding new public trace attributes.

OMP shares prepared extension modules between session-bound factories. A
module-local registry lets child extensions capture model context and collect
nested children while suppressing duplicate child-local terminal snapshots.
Entries are released on terminal lifecycle events or session shutdown. Content
privacy and event/byte limits apply to child snapshots as to parent snapshots.

Validation commands (synthetic data and temporary profiles only):

```bash
node scripts/test-omp-subagents.mjs
node scripts/smoke-omp.mjs "$PWD/obs-agent-connector" "$(command -v omp)" --subagent
node scripts/smoke-omp.mjs "$PWD/obs-agent-connector" "$(command -v omp)" --nested
node scripts/smoke-omp.mjs "$PWD/obs-agent-connector" "$(command -v omp)" --parallel
```

The real OMP smoke decodes uploaded OTLP span identities and checks shared Trace
IDs, delegate-tool parentage, unique span IDs, and child LLM spans. Nested mode
also checks the grandchild relationship. Parallel mode starts two asynchronous
children in one batch task call and verifies both point to the same delegate
span, including provider tool IDs containing `|`. These tests start fresh OMP
processes; they do not imply hot reloading of an already running extension.

## Configuration, Privacy and Recovery

Runtime configuration is read from `~/.obs-agent-connector/omp/gtrace.json`.
Supported fields include `enabled`, `endpoint`, `tracePath`, `metricsPath`,
`headers`, `resourceAttributes`, `captureContent`, `maxChars`/`max_chars`, and
`timeoutMs`/`timeout_ms`. The configured timeout applies to each HTTP attempt (capped at 30 seconds).
All uploads and retry waits for one snapshot share a 30-second deadline.
Default GTrace paths are `v1/write/otel-llm` and `v1/write/otel-metrics`.

Content is sanitized before persistence and again before upload. Images,
provider payloads, and signatures are omitted. `captureContent=none` retains
structural IDs, names, timings and usage while omitting prompt/tool content.
The extension rereads the config on events; disabling telemetry drops the active
in-memory request and stops new queue work. Already running uploads may finish.

Queue snapshots are limited to 4,000 events and approximately 8 MiB per request.
Requests exceeding a bound are dropped rather than exported as complete partial
traces. JS passes the exact snapshot path using `--snapshot` to a dedicated Go
worker. The worker processes only that file; it never scans for pending uploads.
Each signal is attempted at most three times, with 500 ms and 1 s delays for
network errors or HTTP 429/502/503/504, within the shared 30-second deadline.
Other HTTP failures are not retried. Successful signals are not resent during
these retries. The worker deletes its snapshot after processing, whether upload
succeeds, fails, or capture has been disabled. JS also deletes that file on spawn
failure and worker exit. A 35-second JS watchdog kills a hung worker and cleans
up after exit. Fixed-format diagnostics identify the stage and error code without
including payloads or credentials. Go upload diagnostics include the signal and
HTTP status. Failed uploads are never retried in a later session; Trace and
Metrics delivery can be partial. A lost HTTP response can cause a retry of data
already accepted by the backend.

Cleanup runs when the OMP worker is triggered. Temporary snapshots and abandoned
`.json.tmp` files older than 7 days are deleted. Upload
state without a retained snapshot expires after 30 days, except live claims.
Deduplication is therefore limited to the state retention window. The hook log
rotates at approximately 5 MiB and retains one previous file (`.1`). These are
fixed limits with no background cleanup service; inactive installations are
cleaned on their next worker invocation. File expiry uses filesystem modification
time. Temporary files have no aggregate byte cap; process or disk failures can still
leave files until cleanup runs. Cleanup requires a later worker invocation.

## Known Limits

- A crash before terminal snapshot persistence loses the in-memory request.
  There is no transcript replay or background daemon for unfinished requests.
- Failed uploads are discarded after bounded in-process retries. A crash after
  startup can leave a file for expiry cleanup if JS also exits; there is no replay.
- Child collection requires native lifecycle start/end, a matching parent tool
  call ID, and a readable session header within the first 64 KiB of the native
  session file. Missing evidence is omitted rather than linked heuristically.
- Detached children can outlive the parent tool and request. Their native time
  windows are preserved; parent spans are not artificially extended. Closing OMP
  before a child reaches its terminal lifecycle event still loses that child.
- Skill metadata requires readable scalar frontmatter in the captured tool result;
  package.json fallback and multiline YAML metadata are not implemented.
- Reasoning-token breakdowns are not inferred from thinking text.
- The native OMP telemetry subsystem can coexist with this extension. Enabling
  both against the same backend can produce overlapping observations.

## Validation

```bash
go test ./...
go vet ./...
node scripts/test-omp-extension.mjs
node scripts/test-omp-worker.mjs
go build -o obs-agent-connector ./cmd/obs-agent-connector
node scripts/smoke-omp.mjs "$PWD/obs-agent-connector" "$(command -v omp)"
```

The product smoke test uses a temporary HOME and OMP agent directory, a local
synthetic model server, and a local OTLP receiver. It exercises two model calls
with a real `read` tool round trip, dual-signal upload, and lifecycle commands.
It does not use production model credentials or upload production telemetry.
Unit tests cover token normalization, secrets, cancellation/errors, incomplete
tools, synthetic requests, disabled capture, duplicate events, bounded retries and snapshot disposal,
and installer preservation. Native Windows/Linux execution and a production
GTrace backend remain separate validation tasks.
