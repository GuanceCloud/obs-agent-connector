// obs-agent-connector: pi extension v1
// Embedded into the connector and installed without npm dependencies.
import { closeSync, openSync, readFileSync, mkdirSync, statSync, writeFileSync, renameSync, unlinkSync } from "node:fs";
import { basename, dirname, join, resolve, relative, isAbsolute } from "node:path";
import { homedir } from "node:os";
import { randomBytes, randomUUID } from "node:crypto";
import { spawn } from "node:child_process";

const executable = __CONNECTOR_EXECUTABLE__;
const configFile = __CONNECTOR_CONFIG__;
const queueDir = join(dirname(configFile), "state", "queue");
const workerLock = join(dirname(configFile), "state", "pi-worker.lock");
const maxEvents = 4000;
const maxBytes = 8 * 1024 * 1024;
const maxContextMessages = 100;
const maxContextContentMessages = 12;
const staleWorkerLockMs = 2 * 60 * 1000;

function hexID(bytes) { return randomBytes(bytes).toString("hex"); }

let cachedConfig;
let configLoaded = false;

function config(force = false) {
  if (configLoaded && !force) return cachedConfig;
  configLoaded = true;
  try {
    const value = JSON.parse(readFileSync(configFile, "utf8"));
    cachedConfig = value.enabled === true ? value : null;
  } catch { cachedConfig = null; }
  return cachedConfig;
}

function redact(text) {
  return text
    .replace(/-----BEGIN[ \t]+(?:[A-Z0-9]+[ \t]+)*PRIVATE[ \t]+KEY-----[\s\S]*?(?:-----END[ \t]+(?:[A-Z0-9]+[ \t]+)*PRIVATE[ \t]+KEY-----|$)/g, "[REDACTED PRIVATE KEY]")
    .replace(/\b(authorization|cookie)\s*:\s*[^\r\n]+/gi, "$1: [REDACTED]")
    .replace(/\bBearer\s+[A-Za-z0-9._~+/=-]+/gi, "Bearer [REDACTED]")
    .replace(/\b(password|passwd|secret|token|api[-_ ]?key|access[-_ ]?key)\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;]+)/gi, "$1=[REDACTED]")
    .replace(/\b(sk-[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9_]{12,})\b/g, "[REDACTED]");
}

function charLength(value) {
  if (typeof value !== "string") return 0;
  return /[\uD800-\uDFFF]/.test(value) ? Array.from(value).length : value.length;
}

function sanitize(value, cfg, depth = 0) {
  if (cfg.captureContent === "none" || cfg.capture_content === "none") return undefined;
  if (depth >= 8) return "[TRUNCATED_DEPTH]";
  const limit = Math.min(100000, Math.max(1, Number(cfg.maxChars ?? cfg.max_chars) || 20000));
  if (typeof value === "string") {
    const clean = redact(value);
    return clean.length <= limit ? clean : Array.from(clean).slice(0, limit).join("");
  }
  if (Array.isArray(value)) return value.slice(0, 100).map(item => sanitize(item, cfg, depth + 1));
  if (value && typeof value === "object") {
    const out = {};
    for (const [key, item] of Object.entries(value).slice(0, 100)) {
      out[key] = /(authorization|cookie|password|passwd|secret|token|api[-_]?key|credential|private[-_]?key)/i.test(key)
        ? "[REDACTED]" : sanitize(item, cfg, depth + 1);
    }
    return out;
  }
  return value;
}

function content(value, cfg) {
  if (typeof value === "string") return sanitize(value, cfg);
  if (!Array.isArray(value)) return undefined;
  return value.slice(0, 100).map(part => {
    if (part.type === "text") return { type: "text", text: sanitize(part.text, cfg) };
    if (part.type === "thinking") return { type: "thinking", thinking: sanitize(part.thinking, cfg) };
    if (part.type === "toolCall") return { type: "toolCall", id: part.id, name: part.name, arguments: sanitize(part.arguments, cfg) };
    return { type: "omitted" };
  });
}

function textLength(value) {
  if (typeof value === "string") return charLength(value);
  if (!Array.isArray(value)) return 0;
  return value.reduce((total, part) => total + (part.type === "text" ? charLength(part.text || "") : 0), 0);
}

function message(value, cfg) {
  const hasText = typeof value?.content === "string" ? value.content.trim().length > 0
    : Array.isArray(value?.content) && value.content.some(part => part.type === "text" && part.text?.trim());
  const hasImage = Array.isArray(value?.content) && value.content.some(part => part.type === "image");
  const out = {
    role: value?.role, content: content(value?.content, cfg), timestamp: value?.timestamp,
    toolCallId: value?.toolCallId, synthetic: value?.synthetic, attribution: value?.attribution,
    userInitiated: value?.userInitiated, has_content: Boolean(hasText || hasImage),
    has_text: Boolean(hasText), text_length: textLength(value?.content),
  };
  if (value?.role === "assistant") {
    Object.assign(out, {
      provider: value.provider, model: value.model, responseId: value.responseId,
      stopReason: value.stopReason, errorMessage: sanitize(value.errorMessage, cfg),
    });
    const usage = value.usage || {};
    out.usage = Object.fromEntries(["input", "output", "cacheRead", "cacheWrite"].map(key => [key, Math.max(0, Number(usage[key]) || 0)]));
  }
  return out;
}

function contextMessages(values, cfg) {
  const selected = values.slice(-maxContextMessages);
  const contentStart = Math.max(0, selected.length - maxContextContentMessages);
  return selected.map((value, index) => index >= contentStart ? message(value, cfg) : {
    role: value?.role,
    timestamp: value?.timestamp,
    toolCallId: value?.toolCallId,
    synthetic: value?.synthetic,
    attribution: value?.attribution,
    userInitiated: value?.userInitiated,
    has_content: false,
    has_text: false,
    text_length: textLength(value?.content),
  });
}

function skillIdentity(event, ctx, cfg) {
  if (event.toolName !== "read" || typeof event.args?.path !== "string" || basename(event.args.path) !== "SKILL.md") return undefined;
  const path = resolve(ctx.cwd || process.cwd(), event.args.path);
  const within = root => {
    const rel = relative(root, path);
    return rel !== ".." && !rel.startsWith(".." + (process.platform === "win32" ? "\\" : "/")) && !isAbsolute(rel);
  };
  const agentDir = process.env.PI_CODING_AGENT_DIR ? resolve(process.env.PI_CODING_AGENT_DIR) : join(homedir(), ".pi", "agent");
  const source = within(agentDir) ? "user" : ctx.cwd && within(resolve(ctx.cwd)) ? "workspace" : undefined;
  return { name: redact(basename(dirname(path))).slice(0, 256), path: sanitize(path, cfg), source };
}

export default function (pi) {
  let active;
  let bytes = 0;
  let currentCall;
  let callSequence = 0;

  const report = (stage, code) => console.error(`[obs-agent-connector] agent=pi stage=${stage} code=${String(code || "UNKNOWN").replace(/[^A-Za-z0-9_-]/g, "_")}`);
  const remove = path => { try { unlinkSync(path); } catch (error) { if (error.code !== "ENOENT") report("cleanup", error.code); } };
  const releaseWorkerLock = () => remove(workerLock);
  const launch = path => {
    mkdirSync(dirname(workerLock), { recursive: true, mode: 0o700 });
    try {
      const descriptor = openSync(workerLock, "wx", 0o600);
      closeSync(descriptor);
    } catch (error) {
      if (error.code !== "EEXIST") { report("worker_lock", error.code); return; }
      try {
        if (Date.now() - statSync(workerLock).mtimeMs <= staleWorkerLockMs) return;
        unlinkSync(workerLock);
        const descriptor = openSync(workerLock, "wx", 0o600);
        closeSync(descriptor);
      } catch (retryError) {
        if (retryError.code !== "EEXIST") report("worker_lock", retryError.code);
        return;
      }
    }
    let child;
    try {
      child = spawn(executable, ["hook", "pi", "--config", configFile, "--snapshot", path, "--drain", "--worker-lock", workerLock], { detached: true, stdio: "ignore", windowsHide: true });
    } catch (error) { releaseWorkerLock(); report("spawn", error.code); return; }
    const timer = setTimeout(() => {
      report("timeout", "WORKER_TIMEOUT");
      try { child.kill("SIGKILL"); } catch (error) { report("terminate", error.code); }
    }, 35000);
    timer.unref();
    child.once("error", error => { clearTimeout(timer); releaseWorkerLock(); report("spawn", error.code); });
    child.once("exit", (code, signal) => { clearTimeout(timer); if (code !== 0 || signal) report("exit", signal || code); });
    child.unref();
  };
  const guarded = (handler, refreshConfig = false) => (event, ctx) => {
    try {
      const cfg = config(refreshConfig);
      if (!cfg) { active = undefined; currentCall = undefined; return; }
      handler(event, ctx, cfg);
    } catch {
      active = undefined;
      currentCall = undefined;
    }
  };
  const begin = ctx => {
    const session = ctx.sessionManager.getSessionId();
    if (!active || active.session_id !== session) {
      const turnID = randomUUID();
      active = {
        schema: 1, session_id: session, turn_id: turnID,
        trace_id: hexID(16), root_span_id: hexID(8),
        started_at: Date.now(), events: [],
      };
      bytes = 0;
      currentCall = undefined;
      callSequence = 0;
    }
  };
  const append = event => {
    if (!active || active.overflow) return;
    bytes += Buffer.byteLength(JSON.stringify(event));
    if (active.events.length >= maxEvents || bytes > maxBytes) { active.overflow = true; active.events = []; return; }
    active.events.push(event);
  };
  const persist = snapshot => {
    if (snapshot.overflow) return;
    delete snapshot.first_tokens;
    snapshot.ended_at = Date.now();
    mkdirSync(queueDir, { recursive: true, mode: 0o700 });
    const path = join(queueDir, snapshot.turn_id + ".json");
    const temp = path + ".tmp";
    try {
      writeFileSync(temp, JSON.stringify(snapshot), { mode: 0o600 });
      renameSync(temp, path);
      launch(path);
    } catch (error) { remove(temp); report("persist", error.code); }
  };

  pi.on("session_start", guarded(() => { active = undefined; currentCall = undefined; }, true));
  pi.on("before_agent_start", guarded((event, ctx, cfg) => {
    begin(ctx);
    const prompt = typeof event.prompt === "string" ? event.prompt : "";
    append({ type: "input", at: Date.now(), message: {
      role: "user", content: sanitize(prompt, cfg), has_content: prompt.length > 0,
      has_text: prompt.length > 0, text_length: charLength(prompt),
    } });
  }, true));
  pi.on("turn_start", guarded((event, ctx) => { begin(ctx); append({ type: "turn_start", at: event.timestamp || Date.now() }); }));
  pi.on("context", guarded((event, ctx, cfg) => {
    begin(ctx);
    if (active?.overflow) return;
    append({ type: "context", at: Date.now(), messages: contextMessages(event.messages, cfg) });
  }));
  pi.on("message_start", guarded((event, ctx, cfg) => {
    if (!["user", "developer", "custom"].includes(event.message?.role)) return;
    begin(ctx);
    if (active?.overflow) return;
    append({ type: "input", at: Date.now(), message: message(event.message, cfg) });
  }));
  pi.on("before_provider_request", guarded((_event, ctx) => {
    begin(ctx);
    currentCall = `llm-${++callSequence}`;
    append({ type: "llm_start", at: Date.now(), call_id: currentCall });
  }));
  pi.on("after_provider_response", guarded((event) => {
    if (currentCall) append({ type: "llm_response", at: Date.now(), call_id: currentCall, status_code: event.status });
  }));
  pi.on("message_update", guarded((event) => {
    if (!currentCall || !event.assistantMessageEvent || active?.first_tokens?.[currentCall]) return;
    const kind = event.assistantMessageEvent.type;
    if (kind !== "text_delta" && kind !== "thinking_delta" && kind !== "toolcall_delta") return;
    active.first_tokens ||= {};
    active.first_tokens[currentCall] = true;
    append({ type: "first_token", at: Date.now(), call_id: currentCall });
  }));
  pi.on("message_end", guarded((event, _ctx, cfg) => {
    if (event.message?.role !== "assistant") return;
    if (active?.overflow) { currentCall = undefined; return; }
    append({ type: "assistant", at: Date.now(), call_id: currentCall, message: message(event.message, cfg) });
    currentCall = undefined;
  }));
  pi.on("tool_execution_start", guarded((event, ctx, cfg) => {
    begin(ctx);
    if (active?.overflow) return;
    const spanID = hexID(8);
    append({ type: "tool_start", at: Date.now(), call_id: event.toolCallId, span_id: spanID, name: event.toolName, args: sanitize(event.args, cfg), skill: skillIdentity(event, ctx, cfg) });
  }));
  pi.on("tool_execution_end", guarded((event, _ctx, cfg) => {
    if (active?.overflow) return;
    append({ type: "tool_end", at: Date.now(), call_id: event.toolCallId, name: event.toolName, result: sanitize(event.result, cfg), is_error: event.isError });
  }));
  pi.on("agent_settled", guarded((_event, _ctx, cfg) => {
    if (!active) return;
    const snapshot = active;
    active = undefined;
    currentCall = undefined;
    persist(snapshot);
  }, true));
  pi.on("session_shutdown", () => { active = undefined; currentCall = undefined; });
}
