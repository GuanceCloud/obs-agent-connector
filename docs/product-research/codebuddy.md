# CodeBuddy Telemetry Product Research

## Product and Evidence

- Product: Tencent Cloud CodeBuddy / WorkBuddy Enterprise IDE Agent.
- Validated build: Linux x64 `4.10.4.33993995-1ba59196-cn`, based on VS Code `1.106.1`.
- Product-level validation currently covers Linux x64. The connector exposes the built-in adapter on Linux, macOS, and Windows because the Hook, settings, transcript, and connector paths are platform-neutral; macOS and Windows still require package-level validation.
- Evidence date: 2026-08-03. Committed fixtures are synthetic and contain no user transcript or credential.

## Hook and Data Sources

CodeBuddy supports command Hooks delivered as JSON on stdin. The adapter uses only:

- `Stop` for completed turns;
- `SessionEnd` for cancellation and missed-Stop recovery;
- the Hook `transcript_path`, which the validated product resolves to a conversation `index.json`;
- `<conversation>/messages/<id>.json` records referenced by each request.

`session_id` is stable for a conversation. `generation_id` matches the request ID in `index.json` and is the turn identifier. Message and tool call IDs are stable within that request. Unknown transcript basenames, missing IDs, missing message files, and malformed records are rejected rather than inferred.

## Terminal and Timing Semantics

- `request.state=complete` is the authoritative normal terminal state. A final assistant message may still have `isComplete=false`, so that flag is diagnostic only.
- A `Stop` request that is still running is not uploaded. The Hook queues a background worker and returns immediately; the worker waits for the request to become terminal.
- At `SessionEnd`, only the final non-terminal request may be mapped to `cancelled`; already completed requests can be replayed and are deduplicated.
- Request and message timestamps provide inferred turn, LLM, tool, and assistant windows. Generated spans carry `timing.source=inferred`.

## LLM, Tool, Skill, and Subagent Limits

CodeBuddy does not expose reliable per-LLM call IDs, provider boundaries, TTFT, or streaming timestamps through this collection surface. Each request therefore produces one aggregate `llm` span with request-level token usage. Request/response model fields use the Hook `model` value when present.

Assistant `tool-call` and tool `tool-result` content is paired by `toolCallId`. Tool errors use `isError`. The collection surface does not provide reliable Skill metadata or subagent parent IDs, so the adapter does not create `skill:*` or subagent relationships.

## Architecture and Reliability

The selected architecture is terminal Hook plus transcript replay:

```text
Stop / SessionEnd -> private queue -> background worker -> index/messages
                  -> normalized Turn -> shared semantic builder
                  -> shared Metrics -> OTLP traces and metrics
```

The connector uses one shared, dependency-free Go runtime. State is keyed by `(session_id, generation_id)` and records claim, traces, metrics, and completion separately. A traces-success/metrics-failure retry sends only metrics. Hook and worker failures are fail-open for CodeBuddy.

## Installation, Configuration, and Privacy

- Hook settings: `~/.codebuddy/settings.json`.
- Runtime config: `~/.obs-agent-connector/codebuddy/gtrace.json`.
- Hook log: `~/.obs-agent-connector/codebuddy/gtrace-hooks.json`.
- Queue and upload state: `~/.codebuddy/gtrace/`.
- Installation incrementally merges `Stop` and `SessionEnd`, replaces connector-owned and legacy CodeBuddy OTEL Hooks, and preserves unrelated settings.
- Upgrade reconciles Hooks without rewriting runtime configuration.
- Removal deletes connector-managed config and Hook logs under `~/.obs-agent-connector/codebuddy/` while preserving unrelated Hooks. `--purge-config` also removes legacy Agent-local config and upload state.
- Content capture supports `none`, `preview`, and `full`; values are recursively redacted and bounded. Logs contain hashed identifiers and never include authentication headers, prompt/output text, tool arguments, or tool results.

CodeBuddy contains its own product telemetry facilities. Deployments that enable both pipelines should use stable resource identity such as `service.name` and `agent_runtime=codebuddy` to detect unintended duplicate ingestion.
