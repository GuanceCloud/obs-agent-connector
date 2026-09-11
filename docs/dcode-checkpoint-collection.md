# DCode checkpoint collection

DCode 0.1.68 may write only the user message to its transcript, while assistant usage and tool metadata remain in local checkpoint writes. Parsing that transcript alone produces missing token counts and incomplete LLM inputs/outputs.

The background DCode collector now attempts read-only enrichment before parsing a queued terminal turn. It uses DCode's existing LangChain serializer and transcript redactor, keeps main-thread messages, deduplicates message IDs, and preserves usage, native tool calls, and tool response IDs. It writes an atomic mode-0600 companion `.otel.jsonl` file without changing the original transcript. The existing managed hook commands do not need to change.

Python discovery uses `DCODE_OTEL_PYTHON` when set, otherwise Python beside the resolved `dcode` executable, then `python3` on PATH. The interpreter must have DCode's dependencies installed. The subprocess is bounded to three seconds and runs in the background worker. Missing dependencies, incompatible checkpoint schemas, unavailable databases, and timeouts fall back to the original transcript.

Each LLM gets the available conversation up to that message, including earlier recorded turns, tool arguments, and tool results. Later answers and child-agent messages are excluded. The displayed input is a role-labeled reconstruction; normalized message arrays are retained as JSON in `dcode.input.messages` and `dcode.output.messages`. This avoids a receiver UI that displays only the first message of an array. Both forms respect content capture and sanitization settings.

Input/output/cache usage is recorded per LLM and summed once on the turn. Input usage already includes cache reads, so cache reads are not added again. Assistant output spans do not carry token usage. Native tool-call IDs take precedence when associating tools with LLMs. LLM timing windows use associated tool boundaries to retain causal order instead of slicing across tool execution.

## Validation

Run normal Go tests and, where DCode is installed, enable the checkpoint integration test:

```sh
go test ./internal/adapters/dcode/...
DCODE_TEST_PYTHON=/path/to/dcode/venv/bin/python go test ./internal/adapters/dcode/checkpoint -v
```

The integration test creates a temporary database and checks usage, tool IDs, redaction, duplicate messages, idempotence, file permissions, original preservation, and fail-open behavior. It does not read the user's sessions database. Parser regressions cover three LLM calls, two tools, preceding conversation, no future/child message leakage, disabled content, exact token totals, structured records, readable previews, and causal timing.

The parser/enrichment path was also verified with a live DCode 0.1.68 call on Linux: two LLMs and one Read operation, input tokens 23,379, output tokens 63, cache reads 22,528. The receiver displayed the first tool call's arguments and the second LLM's user request, tool call, and matching file result. Replaying concurrent terminal hooks did not upload additional spans or metrics. The background-worker integration added here was separately tested with DCode's real Python and a temporary SQLite database.

## Limits

Checkpoint recovery currently targets DCode 0.1.68's private transcript API and checkpoint schema. Future upgrades need integration validation. The reconstructed input is not a captured provider request: injected system prompts, tool schemas, and context compaction may be absent. `input.source=dcode_transcript_reconstruction` identifies this boundary. Durations remain estimates labeled `timing.source=dcode_hook_bounds_estimate`. Existing uploaded traces are not backfilled.
