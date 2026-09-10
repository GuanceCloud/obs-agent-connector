// Run with: node scripts/test-omp-extension.mjs
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync, readdirSync, existsSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const root = mkdtempSync(join(tmpdir(), "connector-omp-extension-"));
try {
  const configFile = join(root, "gtrace.json");
  const cfg = { enabled: true, captureContent: "preview", maxChars: 20000 };
  writeFileSync(configFile, JSON.stringify(cfg));
  const source = readFileSync(new URL("../internal/adapters/omp/bridge/extension.js", import.meta.url), "utf8")
    .replaceAll("__CONNECTOR_EXECUTABLE__", JSON.stringify(process.execPath))
    .replaceAll("__CONNECTOR_CONFIG__", JSON.stringify(configFile));
  const moduleFile = join(root, "extension.mjs");
  writeFileSync(moduleFile, source);
  const { default: register } = await import(pathToFileURL(moduleFile));
  const handlers = new Map();
  register({ on: (name, fn) => handlers.set(name, fn) });
  const ctx = { cwd: root, sessionManager: { getSessionId: () => "synthetic-session" } };
  const emit = (name, value = {}) => handlers.get(name)?.(value, ctx);
  const user = { role: "user", content: "Hello token=private-value", timestamp: 1000 };
  const assistant = { role: "assistant", content: [{ type: "text", text: "Bearer abcdef123456" }],
    timestamp: 1001, stopReason: "stop", usage: { input: 10, output: 2, cacheRead: 3 } };
  emit("agent_start");
  emit("message_start", { message: user });
  emit("context", { messages: [{role:"toolResult",toolCallId:"call-context",content:[{type:"text",text:"synthetic result"}]}] });
  emit("message_end", { message: assistant });
  emit("agent_end", { willContinue: true });
  assert.equal(existsSync(join(root, "state", "queue")), false, "auto retry exported early");
  emit("agent_start");
  emit("message_end", { message: { ...assistant, timestamp: 2000 } });
  emit("agent_end");
  const queue = join(root, "state", "queue");
  assert.equal(readdirSync(queue).length, 1);
  const text = readFileSync(join(queue, readdirSync(queue)[0]), "utf8");
  assert(!text.includes("private-value") && !text.includes("abcdef123456"), "queue leaked secret");
  const snapshot = JSON.parse(text);
  assert.equal(snapshot.events.filter(e => e.type === "assistant").length, 2);
  assert.equal(snapshot.events.find(e => e.type === "assistant").message.usage.cacheRead, 3, "token counts were redacted");
  assert.equal(snapshot.events.find(e => e.type === "context").messages[0].toolCallId, "call-context", "context lost tool result ID");
  emit("agent_end");
  assert.equal(readdirSync(queue).length, 1, "duplicate end created another turn");

  emit("agent_start");
  emit("message_start", { message: user });
  writeFileSync(configFile, JSON.stringify({ ...cfg, captureContent: "none" }));
  emit("message_end", { message: assistant });
  emit("tool_execution_start", { toolName: "read", toolCallId: "skill-none", args: {path: join(root, "skills", "example", "SKILL.md")} });
  emit("tool_execution_end", { toolName: "read", toolCallId: "skill-none", result: {content: [{type:"text",text:"private skill body"}]} });
  emit("agent_end");
  const none = readdirSync(queue).map(p => JSON.parse(readFileSync(join(queue, p), "utf8"))).find(s => s.turn_id !== snapshot.turn_id);
  assert.equal(none.events[0].message.text_length, Array.from(user.content).length);
  const skillEvent = none.events.find(e => e.type === "tool_start");
  assert.deepEqual(skillEvent.skill, {name:"example",source:"workspace"});
  assert.equal(skillEvent.args, undefined);
  assert(!JSON.stringify(none).includes(root));
  assert(none.events[0].message.has_content);
  assert(none.events[1].message.has_text, "content-free assistant lost its text event marker");
  assert(!JSON.stringify(none).includes("Hello"), "policy change retained content");

  const previous = new Set(readdirSync(queue));
  writeFileSync(configFile, JSON.stringify(cfg));
  emit("agent_start");
  emit("message_start", { message: { role: "user", content: [{ type: "image", data: "synthetic-image-bytes", mimeType: "image/png" }] } });
  emit("message_end", { message: assistant });
  emit("agent_end");
  const imageFile = readdirSync(queue).find(p => !previous.has(p));
  const imageBody = readFileSync(join(queue, imageFile), "utf8");
  const imageRequest = JSON.parse(imageBody);
  assert(imageRequest.events[0].message.has_content, "image-only request classified as empty");
  assert.equal(imageRequest.events[0].message.has_text, false);
  assert(!imageBody.includes("synthetic-image-bytes"), "image payload persisted");

  writeFileSync(configFile, JSON.stringify({ ...cfg, enabled: false }));
  emit("agent_start");emit("message_start", { message: user });emit("agent_end");
  assert.equal(readdirSync(queue).length, 3, "disabled extension queued a turn");
  writeFileSync(configFile, JSON.stringify(cfg));
  emit("agent_start");
  for (let i = 0; i < 4001; i++) emit("turn_start", { timestamp: i });
  emit("agent_end");
  assert.equal(readdirSync(queue).length, 3, "overflow became a partial trace");
  console.log("OMP extension tests passed: retry, duplicates, privacy, policy changes, disabled and overflow.");
} finally { rmSync(root, { recursive: true, force: true }); }
