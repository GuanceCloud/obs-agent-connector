# Kiro CLI Telemetry Product Research

## 1. Product Scope

- Product: Kiro CLI, using the v3 Agent engine.
- Locally validated binary: `kiro-cli 2.18.1` on Linux x64, with the v3 engine available through `--v3` / `--agent-engine v3`.
- Supported connector platforms: macOS, Linux, and Windows. Kiro officially supports the CLI on all three; product-level telemetry validation currently covers Linux x64 only.
- Target implementation: the built-in Kiro adapter in `obs-agent-connector`.
- Evidence date: 2026-08-21.

The connector does not instrument Kiro IDE sessions or legacy Kiro CLI v1/v2 Agent-engine sessions. The current integration depends on the v3 global Hook format and the terminal session store.

## 2. Hook Capability

| Item | Conclusion | Evidence |
| --- | --- | --- |
| Extension mechanism | Command Hooks in global or project `.kiro/hooks/*.json` files | Kiro Hooks and configuration-scope documentation |
| Used Hooks | `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, and `Stop` | Current v3 Hook trigger reference |
| Hook input | JSON on stdin, including `session_id` and `cwd`; tool Hooks add tool name/input/response; Stop adds `assistant_response` | Current CLI Hook reference and local payload consumers |
| Timeout and failure | Command Hooks have a bounded timeout; nonzero exits are reported by Kiro | Current Hook action documentation |
| Duplicate/concurrent events | No uniqueness guarantee is assumed | Connector journal locks and `(session_id, turn_id)` upload claims |
| Replay | Session JSONL is replayed after Stop | Local `~/.kiro/sessions/cli` evidence |

## 3. Data Sources

| Data source | Path or entry | Format | Lifecycle | Sensitivity |
| --- | --- | --- | --- | --- |
| Hook | stdin | JSON | One process per event | Prompt, tool arguments, tool results, final response |
| Session transcript | `~/.kiro/sessions/cli/<session_id>.jsonl` | JSONL v1 | Appended during a terminal session | Prompt, assistant output, tool calls/results |
| Session sidecar | `~/.kiro/sessions/cli/<session_id>.json` | JSON | Rewritten as session metadata changes | cwd, model, timing, aggregate usage, result metadata |
| Legacy SQLite | `~/.local/share/kiro-cli/data.sqlite3` | SQLite | Used by older engines | Not read by this adapter |

Observed v3 JSONL records include `Prompt`, `AssistantMessage`, and `ToolResults`. Assistant content contains `text`, `thinking`, and `toolUse` variants. Thinking content is intentionally ignored.

## 4. Identifiers and Correlation

| Concept | Source field | Stability | Fallback |
| --- | --- | --- | --- |
| Session ID | Hook/sidecar `session_id` | Stable for the terminal conversation | No upload without a session ID |
| Turn ID | `result.Ok.id`, then the first `message_ids` entry | Stable in observed sidecars | Hash of session, start time, and prompt |
| Message ID | `AssistantMessage.data.message_id` | Stable inside a session | Derived from turn and assistant index |
| LLM call ID | Assistant message ID | Stable when present | Derived from turn and assistant index |
| Tool call ID | `toolUse.data.toolUseId` | Stable when present | Hook ID or a derived turn-local ID |
| Parent/subagent ID | Unknown | Not available on this surface | No subagent relationship is emitted |

## 5. Lifecycle

- Turn start: `UserPromptSubmit`, backed by a `Prompt` JSONL record.
- Normal completion: `Stop`, backed by sidecar `end_reason=UserTurnEnd` and a final assistant response.
- Cancellation: sidecar end reasons containing cancelled, interrupted, or aborted.
- Error: sidecar end reasons containing error or failed.
- Internal title/summary/heartbeat identification: not required for the v3 session records observed; blank turns and records without assistant/tool evidence are skipped.
- Write ordering: Stop may arrive before the final session update is stable. The worker polls for up to two seconds and can use `assistant_response` as a final-output fallback.

```text
UserPromptSubmit -> journal prompt
PreToolUse       -> journal tool start
PostToolUse      -> journal tool result
Stop             -> snapshot journal -> persistent queue -> return immediately
worker           -> sidecar + JSONL -> normalized terminal Turn
                 -> spans -> metrics -> signal-specific upload state
```

## 6. LLM and Token Data

| Field | Source | Scope | Availability and limit |
| --- | --- | --- | --- |
| Provider | Derived from model name | Call | Omitted when it cannot be identified confidently |
| Request model | Sidecar `model` or `rts_model_state.model_info.model_id` | Turn | Used for each assistant-message call |
| Response model | Same as request model | Turn | No separate response field is available |
| Input token | `input_token_count` | Aggregate turn | Assigned to the LLM span only for single-call turns |
| Output token | `output_token_count` | Aggregate turn | Assigned to the LLM span only for single-call turns |
| Cache read token | `cache_read_input_token_count` | Aggregate turn | Same limitation as input/output tokens |
| Cache creation token | `cache_write_input_token_count` | Aggregate turn | Same limitation as input/output tokens |
| Reasoning token | Unknown | — | Omitted |
| Finish reason | Sidecar end reason plus Stop | Turn | Normal calls use `stop`; terminal status remains separate |
| Start/end | Sidecar end timestamp and turn duration | Turn | Per-call windows are non-overlapping inferred slices |
| TTFT | Unknown | — | Omitted |

Kiro exposes aggregate turn usage rather than verified per-LLM usage. For multi-call tool chains the adapter keeps usage on the root Turn and does not fabricate per-call token allocation.

## 7. Tool, Skill, and Subagent Data

- Tool start/end: Hook receipt timestamps from `PreToolUse` and `PostToolUse`.
- Tool result/error: transcript `ToolResults` first, then `PostToolUse.tool_response`; explicit Hook error flags map to tool error status.
- Command extraction: `command`, `cmd`, or `script` from tool input after recursive redaction and truncation.
- Skill evidence: the current collection surface does not provide a reliable Skill event or path, so no `skill:*` span is emitted.
- Subagent model: unknown on this collection surface.
- Parent relationship: tool spans are direct children of `invoke_agent`; a triggering assistant message is referenced when available.

## 8. Installation and Configuration

| Platform | Product home | Hook file | Session store | Reload |
| --- | --- | --- | --- | --- |
| Linux | `~/.kiro` | `~/.kiro/hooks/obs-agent-connector.json` | `~/.kiro/sessions/cli` | New sessions load the reconciled global Hook file |
| macOS | `~/.kiro` | Same | Same | Same; package-level telemetry validation remains pending |
| Windows | User home `.kiro` | Same logical path | Same logical path | Same; package-level telemetry validation remains pending |

- Official CLI command: `kiro-cli`.
- Registry: standalone v3 Hook JSON; no marketplace is required.
- Runtime config: `~/.obs-agent-connector/kiro/gtrace.json`, with `~/.kiro/gtrace.json` as a migration fallback.
- Product write-back: Kiro owns its session files; the connector owns only its dedicated Hook file, managed config, journal, queue, upload state, and Hook log.
- Native/legacy conflict: legacy v1/v2 engine Hooks and SQLite sessions are outside the supported surface. No public `kiro-otel-plugin` release repository was available during research.
- Sensitive config: endpoint/token headers stay in the mode-0600 managed config and are never logged.

## 9. Architecture Decision

- Pattern: hybrid journal plus terminal replay.
- OTLP: the repository's dependency-free OTLP/HTTP Protobuf encoder.
- Reference patterns: WorkBuddy-style short Hook journal, CodeBuddy-style detached terminal worker, and shared connector state/semantic builders.
- Reason: transcript records provide message/tool identity while Hooks provide tool timing and the authoritative terminal boundary.
- Missing events: transcript-only tools use inferred timing; Stop output fills a missing final transcript write; unknown Skill/subagent/token fields are omitted.
- Deduplication key: `(session_id, turn_id)` plus a normalized Turn fingerprint.
- Partial recovery: traces and metrics are marked independently. A persistent queue stores the normalized Turn before upload, so a later Hook can restart an interrupted worker and retry only the missing signal.

## 10. Field Mapping

| Product field/event | Internal model | Span/attribute | Note |
| --- | --- | --- | --- |
| Prompt | `Turn.InputMessages` | `invoke_agent` input | Redacted and bounded by capture mode |
| AssistantMessage | `LLMCall` | `llm` | Thinking content is excluded |
| ToolUse | `ToolCall` | `tool:<name>` | Tool ID and arguments retained when allowed |
| ToolResults / PostToolUse | `ToolCall.Result` | tool result attributes | Transcript result has precedence |
| Stop assistant response | `AssistantOutput` | `assistant` | Used as a transcript-lag fallback |
| Sidecar turn duration | Turn window | root duration | Authoritative turn timing when present |
| Sidecar token counts | `Turn.Usage` | usage attributes/metrics | Per-call only for a verified single-call turn |

## 11. Fixtures and Tests

All committed fixtures are synthetic and contain no real prompt, user path, endpoint, or credential.

- [x] Normal question/answer
- [x] Multi-LLM tool chain
- [x] Tool success
- [x] Trace success plus metrics failure recovery
- [x] Incomplete final transcript with Stop fallback
- [x] Duplicate-safe upload state
- [x] Content disabled and recursive secret redaction
- [ ] Product-validated tool failure payload
- [ ] Product-validated cancelled/error turn
- [ ] Skill event
- [ ] Subagent relationship
- [ ] macOS and Windows live-session validation

## 12. Unknowns and Risks

| Question | Impact | Current fallback | Follow-up |
| --- | --- | --- | --- |
| Kiro schema changes after the locally validated build | Parser may skip new record variants | Ignore unknown kinds and fail open | Revalidate on Kiro upgrades |
| Aggregate usage for multi-call turns | Per-call token metrics are unavailable | Do not allocate aggregate tokens across calls | Capture a future per-call usage field if Kiro exposes one |
| Tool errors without an explicit error flag | Error status may be absent | Preserve the result and emit normal/unknown status | Capture a sanitized real failure fixture |
| Skill and subagent identity | No skill/subagent spans | Omit unsupported relationships | Revisit when Kiro exposes stable IDs/events |
| IDE session storage | Kiro IDE is not collected | Advertise CLI v3 scope only | Separate IDE research is required |
