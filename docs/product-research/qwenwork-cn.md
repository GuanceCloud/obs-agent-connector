# QwenWork CN local Hooks

The built-in `qwenwork-cn` adapter supports the domestic desktop client profile
`~/.qwenworkcn`. It does not install into the international QwenWork profile.
The real local Hook contract was verified with QwenWork CN 1.2.5 on macOS arm64,
using a personal free account. Enterprise standard and enterprise flagship
organizations were not available for testing; their policies may override local
settings. Enterprise HTTP Hooks are a separate integration and are not installed
by this adapter.

## Setup and lifecycle

Start the domestic desktop client once, then run:

```bash
obs-agent-connector install qwenwork-cn --endpoint https://example.invalid --x-token YOUR_TOKEN
obs-agent-connector status qwenwork-cn
obs-agent-connector disable qwenwork-cn
obs-agent-connector enable qwenwork-cn
obs-agent-connector update qwenwork-cn
obs-agent-connector remove qwenwork-cn --yes
```

The installer adds local command handlers to `~/.qwenworkcn/settings.json`
for SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, PostToolUseFailure,
Stop, and SessionEnd. All were observed in the desktop probe. It preserves unrelated
settings and handlers, including handlers sharing a managed Hook group. Updates
preserve the runtime config. Start a new conversation after installation.

A normal install also enables native token reporting by merging
`QODERCN_EXPOSE_TOKEN_USAGE=1` into `~/.qwenworkcn/.env` (or the selected profile).
Existing values for this key are set to `1`; unrelated dotenv entries are preserved.
Malformed or unsupported dotenv syntax and non-regular `.env` files (including
symlinks) are rejected before installation writes any files.
`update` / `--no-config` leaves this file unchanged, and an explicitly disabled
install does not enable the flag. Removal preserves this user-owned dotenv file.
No raw SDK logging or application-bundle patch is needed. Restart the desktop
client if its existing worker has already loaded environment settings.

This configures the user-level default: an inherited process environment value
takes precedence, and a project-local dotenv file can replace the user-level
dotenv source. Such overrides must also enable the flag. The flag is an internal
SDK capability verified on desktop 1.2.5, not a guarantee for future client versions
or upstream responses that omit usage.

Config, logs and state are owned by the connector under
`~/.obs-agent-connector/qwenwork-cn/`: `gtrace.json`, `gtrace-hooks.json`, and `state/`.
The default remove deletes these managed files and only unregisters connector
handlers. Connector uninstall also unregisters this adapter; `--keep-config`
preserves its managed configuration.

### Read-only diagnostics

`obs-agent-connector status qwenwork-cn` also reports:

- The native token flag in the user profile. The SDK accepts `1`, `true`, or
  `yes`, ignoring case and surrounding whitespace; installation writes `1`.
- Usage presence in the last replay, with a separate numeric-record source
  timestamp. An observed zero is evidence; an absent field is not a measured zero.
- Independent last-attempt outcomes for traces and metrics: `accepted`, `failed`,
  or `checkpoint_failed`. `accepted` means HTTP acceptance, not verified storage.
- The last replay outcome and pending queue-file count (not a count of turns).

The effective desktop-worker environment remains unknown to this offline command;
profile configuration is not proof that project/process overrides are absent.
Observations older than 24 hours are labelled stale. Source age and replay age are
separate, so replaying old data does not imply recent model activity. Missing,
unsupported or malformed metadata is reported as unknown. Existing pre-upgrade
runs need a new replay/upload before diagnostic observations become available.

Diagnostics are optional, content-free, versioned snapshots in
`~/.obs-agent-connector/qwenwork-cn/state/diagnostics-v1/` (or the configured state
directory). Two latest records per observation kind are retained. Status reads
bounded metadata and the profile flag without network requests, transcript scans,
config writes or arbitrary error text. Diagnostic write failures do not block
normal collection or upload.

### Optional thinking capture

Native thinking content is excluded by default. To opt in, set `captureThinking`
to `true` in the managed `gtrace.json`; `capture_thinking` is accepted as an alias
when the canonical key is absent. `QWENWORK_CN_OTEL_CAPTURE_THINKING` overrides the
file setting. Empty or invalid values fail closed. `captureContent: "none"` always
disables thinking capture, even when the separate flag is enabled.

Only observed transcript thinking is eligible, as a `reasoning` part in the
matching LLM output. It is not copied into root/Assistant text, later LLM inputs,
previews, metrics, Hook journals or diagnostics. Signature and opaque/redacted
blocks are never captured. Redaction and character limits apply. The setting does
not control numeric reasoning-token usage. Normal reinstallation preserves the
setting; `update` / `--no-config` preserves configuration bytes. Installing this
feature does not opt existing users into thinking collection.

### Evidence-based Skill spans

The packaged desktop 1.2.5 code confirms native `Skill` with a nonempty `skill`
argument and native `Read` with a `file_path` argument. The adapter recognizes
these exact shapes. File access must target one `SKILL.md` path, either absolute
or resolved using the exact native Hook cwd under the restrictions below. The
verified `mcp__qw-builtin__mcp_workspace_bash` tool is also recognized for a strict
single-file `cat` command; shell operators, substitutions, multiple targets,
listings and plain mentions are not interpreted as Skill use.

Each recognized tool receives its own Skill child and preserves its result/error
status. Explicit invocation names do not cause a scan of user directories. For
file access, the directory name provides a fallback name; a bounded local
frontmatter read can supply name, description and version. Unknown metadata is
omitted. Canonical file locations inside the configured profile's `skills` directory
receive source type `user`; the verified native default is `~/.qwenworkcn/skills`.
This classifies location, not original authorship or distribution provenance;
bundled Skills may have been copied there. Custom connector profiles do not prove
that the desktop honors the same override. Other source types remain unknown.
Reads reject nonregular files, final symlinks and network/device paths,
limit content to 64 KiB and frontmatter to 100 lines, and never execute commands.
Metadata errors do not interrupt collection. Relative paths require an absolute
native cwd from this exact tool's Hook, matched by session, transcript, tool ID
and turn window. Conflicting cwd or mismatched available request/tool metadata
rejects resolution. There is no process-cwd or latest-event fallback. Resolution
must remain inside the canonical cwd; lexical parent traversal and symlink escapes
are rejected. An unavailable VM path is not translated into a guessed host path.
Complex commands and multi-resource invocations remain unsupported. Builtin/plugin
root lookup and name-only metadata lookup remain unverified and are not scanned.

## Collection contract

- Hook callbacks fail open and journal metadata; they never persist Hook prompt,
  response, tool arguments or error text. Background replay reads the transcript
  supplied by the Hook. Stop, SessionEnd and the next UserPromptSubmit trigger replay.
- Explicit `humanInput.text` records define user turns. `requestSetId` (or UUID)
  identifies a turn; Stop's `parent_request_set_id` identifies its completed request.
  Tool result user records and synthetic interruption messages are not new turns.
- Separate thinking/text/tool records may share `message.id`. They form one
  LLM call, with duplicate record UUIDs and repeated content blocks ignored.
  LLM start times are inferred from transcript boundaries; no TTFT is estimated.
- Each LLM call with visible text produces one ordered Assistant output, associated
  with its source LLM span. Fragments are merged without an extra final-output
  duplicate. Tool-only/thinking-only calls do not create visible Assistant outputs.
  Root output remains the final visible text. Assistant spans do not carry tokens
  or create operation/token metrics. When no measured output duration exists, the
  local event uses a labelled synthetic 1 ms window, minimally extending the root
  boundary if required for containment but never extending the LLM duration.
- Numeric usage is merged field by field within a call. The last valid value
  replaces a prior snapshot; snapshots are not summed. Partial/invalid fields do
  not erase observed counts. Only nonnegative safe JSON integers are accepted.
  Same-turn repeated UUIDs can enrich usage without adding content or extending
  call timing. Cross-turn repeated UUIDs are ignored rather than attributed to
  a new call or rewriting already completed history. Allowlisted
  `qwenwork.usage.<field>.observed` booleans retain explicit-zero evidence.
- Pre/post tool IDs correlate measured execution boundaries. Tool-result
  `is_error` and PostToolUseFailure mark errors even when a later response succeeds.
- Cancellation was observed as a structured `toolUseResult` with
  `kind=tool-status`, `status=cancelled`, `toolDenialKind=interrupted`, followed by
  the synthetic interruption marker. A Stop was not emitted; SessionEnd arrived
  when the next prompt recreated the runtime. Such turns are recovered on the next
  replay trigger. SessionEnd alone is not treated as proof of cancellation.
  Recovery also requires a previously observed UserPromptSubmit matching the
  request ID (or an explicitly bounded start when the ID is absent). Unobserved
  historical turns are skipped rather than exported or retained indefinitely.
- Incomplete/malformed terminal data is retained for retry. Stable trace/span IDs,
  per-turn claims and per-signal checkpoints prevent completed trace uploads from
  being repeated when metrics fail. A later replay retries retained queue files.
- Content follows `--capture-content none|preview|full`, character limits and shared
  redaction. System reminders, attachments, raw requestTokenAnchor,
  environment fields and account metadata are excluded. The journal contains local
  session IDs, transcript paths and native cwd, and is stored with restrictive file permissions.
  Thinking is excluded unless separately opted in as described above.

## Limits

The verified transcript model identifier is `flash`; no provider identity is
inferred. Numeric `message.usage` fields are accepted on LLM spans and were verified
with the native token-reporting flag enabled. Default samples without the flag
omitted usage. Opaque requestTokenAnchor strings are not token counts. No tokens
are estimated or attached to the root Agent span.

Model-provider failures without a terminal event, text-only cancellation without
the verified structured markers, and subagent relationships remain unverified.
The adapter conservatively leaves unproven turns pending. Skill metadata is not
inferred from generic commands; supported evidence is described above. The transcript's `version` is reported as the
Agent runtime version; it may differ from the desktop app version. History predating Hook
registration is not assumed complete. A cancelled task can remain local until a
later replay trigger. Windows registration is supported by the shared CLI, but the
desktop runtime probe was macOS only. No cloud-observability delivery is implied
by local Hook feasibility; configure and validate your own endpoint.

Packaged SDK 1.0.46 has real SubagentStart/Stop callers and explicit
`parent_tool_use_id` stamping, but native delivery and accessible child transcripts
have not been accepted end to end. StopFailure and QueryEnd have worker callers
but are absent from the public SDK Hook allowlist. These events are not registered
or treated as terminal evidence by this adapter. They must not be inferred from
time proximity or SessionEnd. See the [Windows acceptance checklist](../qwenwork-windows-acceptance.md)
for the RC runtime checks still assigned to a Windows tester.

The sanitized two-turn fixture under
`internal/adapters/qwenwork/parse/testdata/` preserves the real record structure
without host paths, account identifiers or private system prompts. Tests cover
local OTLP protobuf delivery, retries, duplicate events, concurrent workers,
failure/cancellation, content suppression and config-preserving lifecycle.

Reliability regressions additionally cover three calls across two turns with exact
input/output metric totals, missing/zero/partial/corrected/duplicate usage, invalid
counts, late enrichment without timing inflation, and cross-turn duplicate
isolation. Cache/reasoning values remain trace details and do not create extra
token metric categories. Negative terminal tests ensure that SessionEnd reasons,
provider-error prose, isolated interruption text and malformed tool cancellation
records do not finalize unproven turns. These fixtures do not establish a new
desktop contract for pure-generation cancellation or provider failures.

## Desktop end-to-end verification

### RC synthetic follow-up

A subsequent 2026-10-08 native run on desktop 1.2.5 produced four parent LLM calls,
three text-bearing Assistant outputs, Write/Read/Agent tools and a Skill child of
Read (`rc-synthetic-check`, version `0.0.1`). Backend token metrics matched input
143146/output 1283; native cache-read usage totalled 107968. The three Assistants
were stored/displayed as 1 ms. Isolated native replay verified thinking off/on/none,
parentage, exact wire values and duplicate suppression without changing real config.
The opt-in `hook/rc_native_test.go` requires explicit synthetic fixture paths and
uses only a loopback receiver and temporary state.

The native Agent result includes an explicit child transcript path and child rows
with matching parent tool ID and sidechain identity. This establishes causal source
evidence, not implemented child-span collection or SubagentStart/Stop Hook delivery.
Child usage is not merged into the parent LLM totals.

Known viewer acceptance failure: a scalar Agent result `63` is present in native
records, decoded OTLP, backend attributes and the UI attribute panel, while the
tool-result preview is blank. No parser loss was found. The exact frontend cause
requires viewer-code inspection; no speculative result wrapping or historical
reupload was performed. Pure-generation cancellation, provider terminal failures
and Windows runtime remain separately unverified.

On 2026-10-08, QwenWork CN 1.2.5 on macOS arm64 delivered real local Hook
events to the built-in adapter and gzip-compressed OTLP/HTTP Protobuf to a
loopback-only receiver. Normal tool execution, an expected exit-code-7 failure,
structured cancellation followed by a later prompt, and replay after a receiver
failure were exercised. Trace and Metrics payloads were decoded successfully.
The final build was checked for positive span durations, direct root parentage,
root time containment, and the LLM end boundary at measured tool start.
Repeated Stop events did not produce additional completed uploads.

Install, config-preserving update, disable/enable and removal were exercised
locally. Temporary Hook registrations and loopback configuration were removed
after verification. No production endpoint or enterprise organization was used.
The samples contained no numeric token usage, so no token metric was emitted.

### GTrace ingestion verification

A subsequent 2026-10-08 test used the user-authorized GTrace endpoint and tags.
Two new synthetic turns (a text reply and a turn with successful and failed tool
calls) each received HTTP 200 from both `/v1/write/otel-llm` and
`/v1/write/otel-metrics`. Replaying the last Stop did not add uploads. Both
per-signal checkpoints and completed markers were present. This confirms ingestion
HTTP acceptance, not independently queried backend storage or console rendering.
No numeric `message.usage` was present in this new sample either.

The repository-directory binary initially produced no Hook journal; changing
the registration to the same build in a temporary directory made Hooks work.
The path-related cause is unresolved; it is not evidence of a quoting failure
or a confirmed macOS permission issue. Verification registrations were removed
afterwards, and only local non-credential evidence was retained.

## Token source investigation

Read-only inspection of desktop 1.2.5 found internal usage handling, but no
verified default plaintext per-LLM usage source for this adapter:

- The bundled worker maintains latest/total input, output and cache counters
  in an encrypted state segment. The adapter does not decrypt private state.
- `requestTokenAnchor` contains hashes and request identifiers, not counts.
- The desktop can log raw SDK events with `CLAUDE_RAW_LOG=1`. Its result-event
  handling accepts usage fields, but actual numeric usage in this mode has not
  been verified. This diagnostic setting is off by default and logs full events;
  it was not enabled during the investigation.
- `sub_chats.ext.contextUsageSnapshot.apiUsage` is a latest context snapshot that
  can be replaced by background refresh, not a durable per-call usage ledger.
  Missing fields can be normalized to zero, so zeros are not reliable evidence.
- SDK `getUsageInfo()` reports quota and credits, not per-call input/output tokens.

### Verified native token reporting

Subsequent inspection found `QODERCN_EXPOSE_TOKEN_USAGE`, which preserves numeric
usage in assistant transcript records without enabling full raw SDK logs.
On 2026-10-08 a temporary process-environment probe produced 35,735 input and 29
output tokens. A second probe, launched normally with the user-profile `.env`
setting, produced 35,743 input and 32 output tokens. Both exact pairs were queried
from the backend token metrics. The existing adapter consumed `message.usage`
without parser changes. Input includes full model context, not just the visible
user prompt. These probes do not establish billing-credit consumption.

The installer now configures this native flag. Character-based estimates and
decryption of private state are not used. The numeric-usage probe's trace upload
received HTTP 200, but its Agent-record query returned no rows during verification;
only backend token metrics were independently confirmed for that probe.

After the reliability changes, a normally launched 2026-10-08 17:27 (Asia/Shanghai)
synthetic turn reported 35,791 input and 39 output tokens. Both exact counts were
independently queried from backend metrics. Offline status reported one observed
call, separate accepted trace/metric attempts, completed replay and no pending
queue files. The sample contained explicit zero cache fields but no reasoning
count. Pure-generation cancellation attempts either completed before the stop
action or stalled during client initialization; those attempts do not establish
a supported cancellation pattern. Real provider failures and nonzero
cache/reasoning counts remain unverified.

### Credit-based billing feasibility

The connector already supports Kiro-style aggregate turn billing through
`Turn.CreditUsage`, exported as `gen_ai.usage.credit` on `invoke_agent` only.
Credits must not become token metrics or be copied to every LLM span.

The bundled Qwen SDK emits `result.total_credits` on successful and failed
results, plus per-model credits. This is cumulative session usage, not turn
usage. Its internal accumulator deduplicates billable requests by request ID.
The public result omits the internal `observed` flag, so zero with an empty model
breakdown does not establish that a turn was free. The ordinary Hook payload,
transcript and inspected synthetic-chat SQLite records did not expose credits.

`getUsageInfo()` belongs to a running SDK query and also requests account quota
over the network. Starting a second resumed query is not a read-only attachment
to the desktop session: it initializes a runtime and may alter session state.
It is therefore not used as a background Hook workaround.

A production credit adapter still needs a verified SDK result event bridge or
explicitly approved diagnostic capture. It must maintain ordered, attributable
session snapshots, handle missing baselines and counter resets, and freeze each
turn's delta across retries. Until this source is verified, the adapter omits
credits instead of repeating session totals or inventing a zero value.

With explicit approval, a temporary raw-SDK-log probe was run on the personal
free account on 2026-10-08. One completed synthetic turn returned
`total_credits: 0`, while `modelUsage.flash` had no `credits` field and assistant
usage had no billable-credit evidence. This is compatible with the SDK's
unobserved default and does not establish a true zero-cost turn. A quota tool
returned account-level remaining credits, not attributable turn consumption.

The result's `usage.request_id` matched the final assistant's
`requestTokenAnchor.requestId`, allowing correlation back to the human
`requestSetId`; `command_lifecycle.command_uuid` also matched the human UUID.
Thus correlation is feasible, but measured credit consumption remains unverified.
A second turn could not be completed during client request timeouts. Raw logging
was then disabled by restarting normally, and diagnostic logs were removed from
the active log directory. No raw logs or credentials are stored in this repository.

### Agent output rendering compatibility

Actual Agent records queried through Owl on 2026-10-08 exposed a message encoding
mismatch. Deployed Kiro and Codex examples store role/parts message arrays as JSON
strings. QwenWork's native OTLP ArrayValue/KVListValue attributes were instead
stored as transport wrappers, which the viewer displayed literally. Tool stdout
and model tool-call arguments were correctly assigned, not reversed.

The shared exporter must encode the two message attributes as JSON strings while
preserving already serialized values and unrelated attributes. QwenWork pure-text
LLM previews should be readable text, while mixed/tool-call output retains its
structure. See [the message wire contract](../message-wire-compatibility.md).

A labelled synthetic regression upload after this correction was rejected with
HTTP 403 by the configured receiver. A later normally installed run successfully
uploaded a 17-span turn; Owl queries confirmed all eight input and nine output
message attributes were JSON arrays without transport wrappers. This verifies
backend storage of the repaired encoding, not a separate visual inspection of
the console. Existing uploaded traces are not rewritten by an exporter change.
