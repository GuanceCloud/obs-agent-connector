// obs-agent-connector: omp extension v1
// Embedded into the connector and installed without npm dependencies.
import { readFileSync, openSync, readSync, closeSync, mkdirSync, writeFileSync, renameSync, unlinkSync } from "node:fs";
import { basename, dirname, join, resolve, relative, isAbsolute } from "node:path";
import { homedir } from "node:os";
import { randomUUID } from "node:crypto";
import { spawn } from "node:child_process";

const executable = __CONNECTOR_EXECUTABLE__;
const configFile = __CONNECTOR_CONFIG__;
const queueDir = join(dirname(configFile), "state", "queue");
// Prepared extension factories share this module across OMP child sessions.
// Native lifecycle IDs own child collection; child-local hooks must not duplicate it.
const childSessions = new Map();
const maxEvents = 4000;
const maxBytes = 8 * 1024 * 1024;

function config() {
  try {
    const value = JSON.parse(readFileSync(configFile, "utf8"));
    return value.enabled === true ? value : null;
  } catch { return null; }
}

function redact(text) {
  return text
    .replace(/-----BEGIN[ \t]+(?:[A-Z0-9]+[ \t]+)*PRIVATE[ \t]+KEY-----[\s\S]*?(?:-----END[ \t]+(?:[A-Z0-9]+[ \t]+)*PRIVATE[ \t]+KEY-----|$)/g, "[REDACTED PRIVATE KEY]")
    .replace(/\b(authorization|cookie)\s*:\s*[^\r\n]+/gi, "$1: [REDACTED]")
    .replace(/\bBearer\s+[A-Za-z0-9._~+/=-]+/gi, "Bearer [REDACTED]")
    .replace(/\b(password|passwd|secret|token|api[-_ ]?key|access[-_ ]?key)\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;]+)/gi, "$1=[REDACTED]")
    .replace(/\b(sk-[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9_]{12,})\b/g, "[REDACTED]");
}

function sanitize(value, cfg, depth = 0) {
  if (cfg.captureContent === "none" || cfg.capture_content === "none") return undefined;
  if (depth >= 8) return "[TRUNCATED_DEPTH]";
  const limit = Math.min(100000, Math.max(1, Number(cfg.maxChars ?? cfg.max_chars) || 20000));
  if (typeof value === "string") return Array.from(redact(value)).slice(0, limit).join("");
  if (Array.isArray(value)) return value.slice(0, 100).map(v => sanitize(v, cfg, depth + 1));
  if (value && typeof value === "object") {
    const out = {};
    for (const [key, item] of Object.entries(value).slice(0, 100)) {
      if (/(authorization|cookie|password|passwd|secret|token|api[-_]?key|credential|private[-_]?key)/i.test(key)) out[key] = "[REDACTED]";
      else out[key] = sanitize(item, cfg, depth + 1);
    }
    return out;
  }
  return value;
}

function content(value, cfg) {
  if (typeof value === "string") return sanitize(value, cfg);
  if (!Array.isArray(value)) return undefined;
  // Omit image bytes, provider payloads, signatures and opaque metadata.
  return value.slice(0, 100).map(part => {
    if (part.type === "text") return { type: "text", text: sanitize(part.text, cfg) };
    if (part.type === "thinking") return { type: "thinking", thinking: sanitize(part.thinking, cfg) };
    if (part.type === "toolCall") return { type: "toolCall", id: part.id, name: part.name, arguments: sanitize(part.arguments, cfg) };
    return { type: "omitted" };
  });
}

// Preserve structural facts before content redaction and clipping.
function textLength(value) {
  if (typeof value === "string") return Array.from(value).length;
  if (!Array.isArray(value)) return 0;
  return value.reduce((n, p) => n + (p.type === "text" ? Array.from(p.text || "").length : 0), 0);
}
function skillIdentity(event, ctx, cfg) {
  if (event.toolName !== "read" || typeof event.args?.path !== "string" || basename(event.args.path) !== "SKILL.md") return undefined;
  const path = resolve(ctx.cwd || process.cwd(), event.args.path);
  const within = root => { const rel = relative(root, path); return rel !== ".." && !rel.startsWith(".." + (process.platform === "win32" ? "\\" : "/")) && !isAbsolute(rel); };
  const source = within(process.env.PI_CODING_AGENT_DIR ? resolve(process.env.PI_CODING_AGENT_DIR) : join(homedir(), ".omp")) ? "user" : ctx.cwd && within(resolve(ctx.cwd)) ? "workspace" : undefined;
  return { name: redact(basename(dirname(path))).slice(0, 256), path: sanitize(path, cfg), source };
}

function message(value, cfg) {
  const hasText = typeof value.content === "string" ? value.content.trim().length > 0
    : Array.isArray(value.content) && value.content.some(p => p.type === "text" && p.text?.trim());
  const hasImage = Array.isArray(value.content) && value.content.some(p => p.type === "image");
  const out = { role: value.role, content: content(value.content, cfg), timestamp: value.timestamp,
    toolCallId: value.toolCallId, synthetic: value.synthetic, attribution: value.attribution, userInitiated: value.userInitiated,
    has_content: Boolean(hasText || hasImage), has_text: Boolean(hasText), text_length: textLength(value.content) };
  if (value.role === "assistant") {
    Object.assign(out, { provider: value.provider, model: value.model, responseId: value.responseId,
      stopReason: value.stopReason, duration: value.duration, ttft: value.ttft, completedAt: value.completedAt,
      errorMessage: sanitize(value.errorMessage, cfg) });
    const usage = value.usage || {};
    out.usage = Object.fromEntries(["input", "output", "cacheRead", "cacheWrite"].map(k => [k, Math.max(0, Number(usage[k]) || 0)]));
  }
  return out;
}

function sessionHeader(path) {
  if (typeof path !== "string") return undefined;
  let fd;
  try {
    fd = openSync(path, "r");
    const buffer = Buffer.alloc(65536);
    const n = readSync(fd, buffer, 0, buffer.length, 0);
    const text = buffer.subarray(0, n).toString("utf8");
    for (const line of text.slice(0, text.lastIndexOf("\n")).split("\n")) {
      if (!line.trim()) continue;
      const header = JSON.parse(line);
      if (header.type === "session" && typeof header.id === "string") return header;
    }
  } catch { return undefined; }
  finally { if (fd !== undefined) closeSync(fd); }
}

function childEvent(event, cfg, ctx) {
  const at = Date.now();
  switch (event.type) {
    case "turn_start": return {type:"turn_start", at:event.timestamp || at};
    case "message_start":
      if (["user", "developer", "custom"].includes(event.message?.role)) return {type:"input",at,message:message(event.message,cfg)};
      break;
    case "message_end":
      if (event.message?.role === "assistant") return {type:"assistant",at,message:message(event.message,cfg)};
      break;
    case "tool_execution_start": return {type:"tool_start",at,call_id:event.toolCallId,name:event.toolName,args:sanitize(event.args,cfg),skill:skillIdentity(event,ctx,cfg)};
    case "tool_execution_end": return {type:"tool_end",at,call_id:event.toolCallId,name:event.toolName,result:content(event.result?.content,cfg),is_error:event.isError};
  }
}

export default function (pi) {
  let active;
  let bytes = 0;
  let sessionFile;
  const children = new Map();
  const report = (stage, code) => {
    console.error(`[obs-agent-connector] agent=omp stage=${stage} code=${String(code || "UNKNOWN").replace(/[^A-Za-z0-9_-]/g, "_")}`);
  };
  const remove = path => {
    try { unlinkSync(path); } catch (error) { if (error.code !== "ENOENT") report("cleanup", error.code); }
  };
  const launch = path => {
    let child;
    try {
      child = spawn(executable, ["hook", "omp", "--config", configFile, "--snapshot", path], {
        detached: true, stdio: "ignore", windowsHide: true,
      });
    } catch (error) { remove(path); report("spawn", error.code); return; }
    const timer = setTimeout(() => {
      report("timeout", "WORKER_TIMEOUT");
      try { child.kill("SIGKILL"); } catch (error) { report("terminate", error.code); }
      // Delete only once exit confirms the worker has stopped.
    }, 35000);
    timer.unref();
    child.once("error", error => { clearTimeout(timer); remove(path); report("spawn", error.code); });
    child.once("exit", (code, signal) => {
      clearTimeout(timer);
      remove(path);
      if (code !== 0 || signal) report("exit", signal || code);
    });
    child.unref();
  };
  const guarded = handler => (event, ctx) => {
    try {
      sessionFile = ctx?.sessionManager?.getSessionFile?.();
      if (sessionFile && childSessions.has(sessionFile)) return;
      const cfg = config();
      if (!cfg) { active = undefined; return; }
      handler(event, ctx, cfg);
    } catch {
      // Collection must never change the host's prompt, tools or stop decision.
      active = undefined;
    }
  };
  const begin = ctx => {
    const session = ctx.sessionManager.getSessionId();
    if (!active || active.session_id !== session) {
      active = { schema: 1, session_id: session, turn_id: randomUUID(), trace_id: randomUUID().replaceAll("-", ""), started_at: Date.now(), events: [] };
      bytes = 0;
    }
  };
  const append = event => {
    if (!active || active.overflow) return;
    bytes += Buffer.byteLength(JSON.stringify(event));
    if (active.events.length >= maxEvents || bytes > maxBytes) {
      active.overflow = true;
      active.events = [];
      return;
    }
    active.events.push(event);
  };
  const persist = (snapshot, cfg) => {
    // Re-apply the current capture policy before persisting a turn that may
    // have started under a different policy.
    for (const item of snapshot.events) {
      if (item.message) {
        item.message.content = content(item.message.content, cfg);
        item.message.errorMessage = sanitize(item.message.errorMessage, cfg);
      }
      if (item.messages) {
        for (const value of item.messages) value.content = content(value.content, cfg);
      }
      if (item.skill) item.skill.path = sanitize(item.skill.path, cfg);
      item.args = sanitize(item.args, cfg);
      item.result = sanitize(item.result, cfg);
    }
    snapshot.ended_at ||= Date.now();
    mkdirSync(queueDir, { recursive: true, mode: 0o700 });
    const path = join(queueDir, snapshot.turn_id + ".json");
    const temp = path + ".tmp";
    try {
      writeFileSync(temp, JSON.stringify(snapshot), { mode: 0o600 });
      renameSync(temp, path);
      launch(path);
    } catch (error) { remove(temp); remove(path); report("persist", error.code); }
  };
  const busGuard = handler => payload => {
    try { const cfg = config(); if (cfg) handler(payload, cfg); else { for (const item of children.values()) childSessions.delete(item.file); children.clear(); } }
    catch { /* Native subagent telemetry must not affect task execution. */ }
  };
  const unsubscribers = [];
  if (pi.events?.on) {
    unsubscribers.push(pi.events.on("task:subagent:lifecycle", busGuard((frame,cfg) => {
      if (typeof frame.id !== "string") return;
      if (frame.status === "started") {
        if (children.has(frame.id) || typeof frame.sessionFile !== "string" || !frame.sessionFile) return;
        const owner = childSessions.get(sessionFile)?.snapshot || active;
        if (!owner || owner.overflow || typeof frame.parentToolCallId !== "string" || !owner.events.some(e => e.type === "tool_start" && e.call_id === frame.parentToolCallId)) return;
        const header = sessionHeader(frame.sessionFile);
        const snapshot = {schema:1,session_id:header?.id || "",turn_id:randomUUID(),trace_id:owner.trace_id,
          parent:{session_id:owner.session_id,turn_id:owner.turn_id,tool_call_id:frame.parentToolCallId},
          started_at:Date.now(),events:[]};
        const item = {snapshot,file:frame.sessionFile,cwd:header?.cwd,bytes:0};
        children.set(frame.id,item);
        childSessions.set(frame.sessionFile,item);
        return;
      }
      if (!["completed","failed","aborted"].includes(frame.status)) return;
      const item = children.get(frame.id);
      if (!item) return;
      children.delete(frame.id);
      childSessions.delete(item.file);
      if (item.snapshot.overflow) return;
      item.snapshot.session_id ||= sessionHeader(item.file)?.id;
      if (!item.snapshot.session_id) return;
      item.snapshot.terminal_status = frame.status;
      item.snapshot.ended_at = Date.now();
      persist(item.snapshot,cfg);
    })));
    unsubscribers.push(pi.events.on("task:subagent:event",busGuard((frame,cfg) => {
      const item = children.get(frame.id);
      if (!item || item.snapshot.overflow) return;
      if (!item.snapshot.session_id) { const header = sessionHeader(item.file); if (header) { item.snapshot.session_id = header.id; item.cwd = header.cwd; } }
      const event = childEvent(frame.event,cfg,{cwd:item.cwd});
      if (!event) return;
      item.bytes += Buffer.byteLength(JSON.stringify(event));
      if (item.snapshot.events.length >= maxEvents || item.bytes > maxBytes) { item.snapshot.overflow=true; item.snapshot.events=[]; return; }
      item.snapshot.events.push(event);
    })));
  }
  pi.on("session_shutdown", () => {
    for (const unsubscribe of unsubscribers) unsubscribe?.();
    for (const item of children.values()) childSessions.delete(item.file);
    children.clear();
  });
  pi.on("session_start", guarded(() => { active = undefined; }));
  pi.on("agent_start", guarded((_event, ctx) => begin(ctx)));
  pi.on("turn_start", guarded((event, ctx) => {
    begin(ctx);
    append({ type: "turn_start", at: event.timestamp || Date.now() });
  }));
  pi.on("context", (event, ctx) => {
    const item = childSessions.get(ctx.sessionManager.getSessionFile?.());
    if (!item) return guarded((value, _ctx, cfg) => {
      append({type:"context",at:Date.now(),messages:value.messages.slice(-100).map(m=>message(m,cfg))});
    })(event,ctx);
    try {
      const cfg=config(); if (!cfg || item.snapshot.overflow) return;
      const value={type:"context",at:Date.now(),messages:event.messages.slice(-100).map(m=>message(m,cfg))};
      item.bytes+=Buffer.byteLength(JSON.stringify(value));
      if (item.snapshot.events.length>=maxEvents || item.bytes>maxBytes) {item.snapshot.overflow=true;item.snapshot.events=[];return;}
      item.snapshot.events.push(value);
    } catch { /* Content capture must not affect model context. */ }
  });
  pi.on("message_start", guarded((event, ctx, cfg) => {
    if (event.message.role !== "user" && event.message.role !== "developer" && event.message.role !== "custom") return;
    begin(ctx);
    append({ type: "input", at: Date.now(), message: message(event.message, cfg) });
  }));
  pi.on("message_end", guarded((event, _ctx, cfg) => {
    if (event.message.role === "assistant") append({ type: "assistant", at: Date.now(), message: message(event.message, cfg) });
  }));
  pi.on("tool_execution_start", guarded((event, _ctx, cfg) => {
    append({ type: "tool_start", at: Date.now(), call_id: event.toolCallId, name: event.toolName, args: sanitize(event.args, cfg), skill: skillIdentity(event, _ctx, cfg) });
  }));
  pi.on("tool_execution_end", guarded((event, _ctx, cfg) => {
    append({ type: "tool_end", at: Date.now(), call_id: event.toolCallId, name: event.toolName,
      result: content(event.result?.content, cfg), is_error: event.isError });
  }));
  pi.on("agent_end", guarded((event, _ctx, cfg) => {
    if (!active || event.willContinue) return;
    const snapshot = active;
    active = undefined;
    if (snapshot.overflow) return;
    persist(snapshot, cfg);
  }));
}
