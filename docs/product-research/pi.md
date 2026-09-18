# Pi Telemetry Product Research

## Scope

- Product: Pi Coding Agent
- Baseline: `@earendil-works/pi-coding-agent` 0.85.1
- Research date: 2026-09-18
- Platforms: macOS, Linux, and Windows
- Adapter: built into `obs-agent-connector`

The event contract and settings behavior were checked against the current official Pi documentation and the 0.85.1 package metadata. The connector has synthetic extension, parser, installer, OTLP, and cross-build coverage. Real Pi conversation and linked independent-child-process runtime smokes have run on macOS; backend trace assembly and Windows runtime smokes remain required before the adapter is described as fully product-validated.

## Extension and data sources

Pi loads user extensions from paths in `~/.pi/agent/settings.json` and supports `/reload`. The connector writes its extension to `~/.obs-agent-connector/pi/extension.js`, then merges that absolute path into the global `extensions` array. It does not replace other settings or extension entries.

The adapter uses the native lifecycle API:

```text
before_agent_start
  -> turn_start
  -> context
  -> before_provider_request
  -> after_provider_response
  -> first qualifying message_update
  -> message_end
  -> tool_execution_start / tool_execution_end as applicable
  -> agent_settled
```

`agent_end` is not terminal because automatic retry, compaction retry, or queued continuation may follow it. The extension persists and launches the worker only from `agent_settled`.

## Identity and timing

| Concept | Source | Behavior |
| --- | --- | --- |
| Session ID | `ctx.sessionManager.getSessionId()` | Stable conversation identity |
| Request ID | Connector-generated UUID at `before_agent_start` | Stable queue, state, and deduplication key |
| LLM call ID | Connector sequence at `before_provider_request` | Correlates provider, first-token, and assistant events |
| Tool call ID | `event.toolCallId` | Native Tool correlation |
| LLM start | `before_provider_request` | Application-side request boundary |
| Response received | `after_provider_response` | HTTP response availability; not treated as completion |
| TTFT | First text, thinking, or tool-call `message_update` minus LLM start | Omitted when no qualifying update is observed |
| LLM end | Assistant `message_end` | Does not include later Tool execution |
| Request end | `agent_settled` | No automatic continuation remains |

No duration or TTFT is apportioned from token counts. Missing event evidence leaves the field absent or zero in the internal model.

## Trace and metrics

The extension writes a bounded, sanitized terminal snapshot. A detached built-in worker normalizes it into one `invoke_agent` root with direct `llm`, `tool:*`, and `assistant` children. A high-confidence `skill:*` child is attached to the matching `read` Tool only when the requested basename is `SKILL.md`.

The worker derives the standard four metrics from the same spans:

- `gen_ai.workflow.duration`
- `gen_ai.agent.operation.count`
- `gen_ai.agent.operation.duration`
- `gen_ai.client.token.usage`

Input token usage includes the native input, cache-read, and cache-write values. Cache counts remain separately available on Trace spans.

## Privacy and reliability

- `enabled=false` exits before request state is created.
- `captureContent=none` retains topology, timing, token usage, Tool identity, and Skill identity while omitting captured content and paths.
- Values are recursively bounded and redact credential-shaped keys and common secret text.
- Snapshots are written atomically with per-turn size and event limits.
- Configuration is refreshed at session, request-start, and terminal boundaries instead of on streaming deltas.
- A terminal event starts one worker for the current snapshot and at most one bounded historical retry, preventing upload-process bursts.
- The Go worker validates that input is a regular JSON file in the Pi-managed queue.
- Persistent upload state prevents duplicate Trace and Metrics delivery and permits signal-specific retry after partial success.
- Collection and upload failures do not change Pi prompts, Tool results, or stop behavior.

## Skill behavior

Skill detection requires a `read` Tool call whose resolved basename is `SKILL.md`. Source is classified as `user` when the path is under the active Pi Agent directory and as `workspace` when it is under the current working directory. Name and source survive `captureContent=none`; path and body do not. Description and version are read only from unambiguous scalar frontmatter already returned by the Tool.

## Subagent behavior

Pi's official Subagent example launches an independent `pi --mode json -p --no-session` process. The parent knows the relationship because its `subagent` Tool owns the process and returns structured `details.results[]` data. The official extension does not provide a native parent trace or Tool-call identifier.

The connector assigns the parent turn Trace ID and `tool:subagent` Span ID before Tool execution. During that exact Tool lifecycle it exposes a bounded W3C trace context and the native parent Tool call ID to child processes. Each child connector extension records that context with its own generated child run ID. The child `invoke_agent` root therefore shares the parent Trace ID and points to the actual `tool:subagent` span. Child LLM, Tool, TTFT, and token data come only from the child's native lifecycle events; the parent retains only bounded result summaries and does not add child usage again.

Official single, chain, and parallel modes use one outer `subagent` Tool call, so every spawned child points to that delegate span while retaining a unique root Span ID and child run ID. Nested calls repeat the same mechanism. If multiple distinct `subagent` Tool calls overlap in one Pi tool batch, the connector suppresses ambient propagation and leaves those children as standalone traces instead of guessing a relationship. Exact child process startup time remains represented by the parent Tool duration rather than a fabricated child span boundary.

## Installation and upgrade

| Item | Path or behavior |
| --- | --- |
| Global Pi settings | `~/.pi/agent/settings.json` |
| Agent directory override | `PI_CODING_AGENT_DIR` |
| Managed extension | `~/.obs-agent-connector/pi/extension.js` |
| Runtime configuration | `~/.obs-agent-connector/pi/gtrace.json` |
| Queue and upload state | `~/.obs-agent-connector/pi/state/` |
| Reload | Start a new Pi session or run `/reload` |

Install and update use the same merge path. `update pi` preserves runtime configuration through `--no-config`. Removal deletes only the managed extension entry and connector-owned files; unrelated Pi settings and extensions remain.

## Evidence and remaining validation

Sources:

- [Pi extension documentation](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md)
- [Pi settings documentation](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/settings.md)
- [Pi Subagent example](https://github.com/earendil-works/pi/tree/main/packages/coding-agent/examples/extensions/subagent)

Implemented fixtures cover multi-LLM Tool flow, native TTFT, token and cache usage, Skill metadata, cancellation, incomplete Tool suppression, content-free capture, Subagent result summaries, explicit parent-child Trace context, ambiguous concurrent-call fallback, duplicate terminal events, disabled collection, and upload deduplication. A real Pi Subagent runtime smoke completed both child and parent uploads. Backend trace assembly and Windows runtime loading remain open validation items.
