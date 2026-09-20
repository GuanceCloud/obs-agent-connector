# Pi Telemetry Product Research

## Scope

- Product: Pi Coding Agent
- Baseline: `@earendil-works/pi-coding-agent` 0.85.1
- Research date: 2026-09-18
- Platforms: macOS, Linux, and Windows
- Adapter: built into `obs-agent-connector`

The event contract and settings behavior were checked against the current official Pi documentation and the 0.85.1 package metadata. The connector has synthetic extension, parser, installer, OTLP, and cross-build coverage. Real Pi conversation and independent child-process runtime smokes have run on macOS; backend and Windows runtime smokes remain required before the adapter is described as fully product-validated.

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
- Each provider context retains metadata for the latest 100 messages and captured content for only the latest 12, bounding synchronous work on Pi's event thread while preserving native aggregate token usage.
- Snapshots are written atomically with per-turn size and event limits.
- Configuration is refreshed at session, request-start, and terminal boundaries instead of on streaming deltas.
- A terminal event atomically elects one worker. That worker processes the current snapshot first and then drains at most eight queued snapshots serially under one 30-second deadline.
- The Go worker validates that input is a regular JSON file in the Pi-managed queue.
- Persistent upload state prevents duplicate Trace and Metrics delivery and permits signal-specific retry after partial success.
- Collection and upload failures do not change Pi prompts, Tool results, or stop behavior.

## Skill behavior

Skill detection requires a `read` Tool call whose resolved basename is `SKILL.md`. Source is classified as `user` when the path is under the active Pi Agent directory and as `workspace` when it is under the current working directory. Name and source survive `captureContent=none`; path and body do not. Description and version are read only from unambiguous scalar frontmatter already returned by the Tool.

## Subagent behavior

Pi's official Subagent example launches an independent `pi --mode json -p --no-session` process. The parent `subagent` Tool records its native duration and bounded result summary, including child count, failed count, and observed models. Each child Pi process records its own native LLM and Tool lifecycle as a separate trace.

The connector does not propagate parent trace context into child processes and does not infer parentage from timing or result text. Single, parallel, chain, and nested Subagent runs therefore remain independent traces. Exact child process startup time is represented only within the parent `tool:subagent` duration.

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

Implemented fixtures cover multi-LLM Tool flow, native TTFT, token and cache usage, Skill metadata, cancellation, incomplete Tool suppression, content-free capture, Subagent result summaries, duplicate terminal events, disabled collection, upload deduplication, absence of Subagent context propagation, and serialized queue draining. A real Pi Subagent runtime smoke completed independent child and parent uploads. Backend validation and Windows runtime loading remain open validation items.
