# Agent message wire compatibility

## 1. Scope and trigger

This contract covers the boundary between the connector's structured internal
messages, OTLP export, and the Guance Agent viewer. It applies to all built-in
adapters that use the shared encoder, not just QwenWork.

## 2. Signatures

The OTLP trace builder accepts `model.Span` attributes. The message keys are
`gen_ai.input.messages` and `gen_ai.output.messages`. Guance exposes them as
`gen_ai_input_messages` and `gen_ai_output_messages` in `AM::agent_trace`.

## 3. Contracts

- Keep structured messages in the internal model, including `role`, ordered
  `parts`, text, and tool-call identifiers and arguments.
- Encode structured values of the two message attributes as JSON in OTLP
  `stringValue`. Preserve existing strings without an extra JSON encoding layer.
- Do not apply this compatibility conversion to unrelated arrays or objects.
- Apply capture controls, recursive redaction, and content limits before export.
- QwenWork text-only LLM previews contain readable text. Mixed/tool-call previews
  retain structured details. Tool stdout remains text; JSON is not mandatory.
- No changes to metric types, token accounting, span parentage, or timing are
  implied by this encoding contract.

## 4. Validation and error matrix

| Input | Required behavior |
| --- | --- |
| Structured message array | JSON string preserving message shape |
| Already serialized message string | Unchanged, not double encoded |
| Content capture disabled | No captured message bodies or previews |
| Sensitive nested content | Redacted before wire encoding |
| Unrelated map/array attribute | Existing OTLP encoding retained |

## 5. Good, base, and bad cases

- Good: mixed text and `tool_call` parts survive JSON decoding in their order.
- Base: a text-only assistant reply displays its text preview.
- Bad: a message field decodes to `values` / `kvlistValue` transport wrappers
  instead of a role/parts message array.

## 6. Required tests

Verify JSON message payloads inside Protobuf exports, pre-serialized input, unrelated attribute
encoding, text-only and mixed QwenWork previews, capture-none, redaction, and
truncation. Backend verification must inspect stored fields, not just HTTP 200.
Use synthetic fixtures; do not commit real demo conversation bodies.

## 7. Wrong versus correct

Wrong message field: `{"values":[{"kvlistValue":{"values":[]}}]}`.

Correct message field: `[{"role":"assistant","parts":[{"type":"text","content":"OK"}]}]`
encoded once as an OTLP string.

This compatibility choice was verified against deployed Kiro and Codex Agent
records on 2026-10-08. Native OTLP arrays/maps are not inherently invalid; the
conversion addresses this receiver/viewer contract specifically. An empty LLM
namespace query is not evidence that Agent data is absent: discover the Agent
viewer's actual AM namespace and verify query success and returned data.
