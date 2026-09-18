// Run with: node scripts/test-pi-extension.mjs
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync, readdirSync, existsSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const root = mkdtempSync(join(tmpdir(), "connector-pi-extension-"));
try {
  const configFile = join(root, "gtrace.json");
  const cfg = { enabled: true, captureContent: "preview", maxChars: 20000 };
  writeFileSync(configFile, JSON.stringify(cfg));
  const source = readFileSync(new URL("../internal/adapters/pi/bridge/extension.js", import.meta.url), "utf8")
    .replaceAll("__CONNECTOR_EXECUTABLE__", JSON.stringify(process.execPath))
    .replaceAll("__CONNECTOR_CONFIG__", JSON.stringify(configFile));
  const moduleFile = join(root, "extension.mjs");
  writeFileSync(moduleFile, source);
  const { default: register } = await import(pathToFileURL(moduleFile));
  const handlers = new Map();
  register({ on: (name, fn) => handlers.set(name, fn) });
  for (const name of ["before_agent_start", "before_provider_request", "after_provider_response", "message_update", "agent_settled"]) {
    assert(handlers.has(name), `missing ${name} handler`);
  }
  const ctx = { cwd: root, sessionManager: { getSessionId: () => "synthetic-session" } };
  const emit = (name, value = {}) => handlers.get(name)?.(value, ctx);
  const user = { role: "user", content: "Hello token=private-value", timestamp: 1000 };
  const assistant = { role: "assistant", content: [{ type: "text", text: "Bearer abcdef123456" }], timestamp: 1001,
    provider: "synthetic", model: "model", responseId: "response-1", stopReason: "stop", usage: { input: 10, output: 2, cacheRead: 3 } };

  emit("before_agent_start", { prompt: user.content });
  emit("message_start", { message: user });
  emit("turn_start", { timestamp: 1000 });
  emit("context", { messages: [user] });
  emit("before_provider_request", { payload: {} });
  emit("after_provider_response", { status: 200, headers: {} });
  emit("message_update", { assistantMessageEvent: { type: "text_delta", delta: "A" } });
  emit("message_update", { assistantMessageEvent: { type: "text_delta", delta: "B" } });
  emit("message_end", { message: assistant });
  assert.equal(existsSync(join(root, "state", "queue")), false, "turn exported before agent_settled");
  emit("agent_settled");

  const queue = join(root, "state", "queue");
  assert.equal(readdirSync(queue).length, 1);
  const text = readFileSync(join(queue, readdirSync(queue)[0]), "utf8");
  assert(!text.includes("private-value") && !text.includes("abcdef123456"), "queue leaked secret");
  const snapshot = JSON.parse(text);
  assert.equal(snapshot.events.filter(event => event.type === "first_token").length, 1, "first token was duplicated");
  assert(snapshot.events.some(event => event.type === "llm_start") && snapshot.events.some(event => event.type === "llm_response"));
  emit("agent_settled");
  assert.equal(readdirSync(queue).length, 1, "duplicate settled event created another turn");

  emit("before_agent_start", { prompt: "Delegate the task" });
  emit("message_start", { message: { ...user, content: "Delegate the task" } });
  emit("tool_execution_start", { toolName: "subagent", toolCallId: "subagent-call-1", args: { agent: "worker" } });
  const propagatedTraceparent = process.env.OBS_AGENT_CONNECTOR_PI_TRACEPARENT;
  assert.match(propagatedTraceparent, /^00-[0-9a-f]{32}-[0-9a-f]{16}-01$/);
  assert.equal(process.env.OBS_AGENT_CONNECTOR_PI_PARENT_TOOL_CALL_ID, "subagent-call-1");

  emit("tool_execution_start", { toolName: "subagent", toolCallId: "subagent-call-2", args: { agent: "worker" } });
  assert.equal(process.env.OBS_AGENT_CONNECTOR_PI_TRACEPARENT, undefined, "ambiguous concurrent subagent calls must not share context");
  emit("tool_execution_end", { toolName: "subagent", toolCallId: "subagent-call-2", result: { details: { results: [] } } });
  assert.equal(process.env.OBS_AGENT_CONNECTOR_PI_TRACEPARENT, propagatedTraceparent, "remaining subagent context was not restored");

  const childHandlers = new Map();
  const childModule = await import(`${pathToFileURL(moduleFile).href}?child=1`);
  childModule.default({ on: (name, fn) => childHandlers.set(name, fn) });
  const childCtx = { cwd: root, sessionManager: { getSessionId: () => "synthetic-child-session" } };
  const emitChild = (name, value = {}) => childHandlers.get(name)?.(value, childCtx);
  emitChild("before_agent_start", { prompt: "Task: inspect README" });
  emitChild("message_start", { message: { ...user, content: "Task: inspect README" } });
  emitChild("before_provider_request", { payload: {} });
  emitChild("message_end", { message: { ...assistant, responseId: "child-response" } });
  emitChild("agent_settled");

  emit("tool_execution_end", { toolName: "subagent", toolCallId: "subagent-call-1", result: { details: { results: [{ exitCode: 0, model: "synthetic/model" }] } } });
  assert.equal(process.env.OBS_AGENT_CONNECTOR_PI_TRACEPARENT, undefined, "trace context leaked after subagent completion");
  assert.equal(process.env.OBS_AGENT_CONNECTOR_PI_PARENT_TOOL_CALL_ID, undefined, "parent tool ID leaked after subagent completion");
  emit("message_end", { message: { ...assistant, responseId: "parent-response" } });
  emit("agent_settled");

  const bridgeSnapshots = readdirSync(queue).map(path => JSON.parse(readFileSync(join(queue, path), "utf8")));
  const parentSnapshot = bridgeSnapshots.find(item => item.session_id === "synthetic-session" && item.events.some(event => event.call_id === "subagent-call-1"));
  const childSnapshot = bridgeSnapshots.find(item => item.session_id === "synthetic-child-session");
  const parentTool = parentSnapshot.events.find(event => event.type === "tool_start" && event.call_id === "subagent-call-1");
  assert.equal(childSnapshot.trace_id, parentSnapshot.trace_id);
  assert.equal(childSnapshot.parent_span_id, parentTool.span_id);
  assert.equal(childSnapshot.parent_tool_call_id, "subagent-call-1");
  assert(childSnapshot.child_run_id && childSnapshot.child_run_id === childSnapshot.turn_id);

  emit("before_agent_start", { prompt: user.content });
  emit("message_start", { message: user });
  emit("before_provider_request", { payload: {} });
  writeFileSync(configFile, JSON.stringify({ ...cfg, captureContent: "none" }));
  emit("message_end", { message: assistant });
  emit("tool_execution_start", { toolName: "read", toolCallId: "skill-none", args: { path: join(root, "skills", "example", "SKILL.md") } });
  emit("tool_execution_end", { toolName: "read", toolCallId: "skill-none", result: { content: [{ type: "text", text: "private skill body" }] } });
  emit("agent_settled");
  const none = readdirSync(queue).map(path => JSON.parse(readFileSync(join(queue, path), "utf8")))
    .find(item => item.events.some(event => event.call_id === "skill-none"));
  assert(none.events.find(event => event.type === "input").message.has_content);
  const skill = none.events.find(event => event.type === "tool_start").skill;
  assert.deepEqual(skill, { name: "example", source: "workspace" });
  assert(!JSON.stringify(none).includes(root) && !JSON.stringify(none).includes("Hello"), "capture none retained content or paths");

  writeFileSync(configFile, JSON.stringify({ ...cfg, enabled: false }));
  emit("before_agent_start"); emit("message_start", { message: user }); emit("agent_settled");
  assert.equal(readdirSync(queue).length, 4, "disabled extension queued a turn");
  console.log("Pi extension tests passed: terminal gating, native LLM timing, subagent propagation, privacy, policy changes and disabled mode.");
} finally {
  rmSync(root, { recursive: true, force: true });
}
