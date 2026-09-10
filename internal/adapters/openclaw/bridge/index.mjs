import { spawn } from 'node:child_process';
import { readFileSync, appendFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';

// No telemetry SDK or npm installation is required in the host process.
export function registerBridge(api, launch = spawn, now = Date.now) {
  const runtime = JSON.parse(readFileSync(new URL('./runtime.json', import.meta.url), 'utf8'));
  // Keep the same JSONL envelope as core/hooklog; never log payloads or raw errors.
  const log = (message, extra) => {
    try {
      const file = runtime.logFile || join(dirname(runtime.configFile), 'gtrace-hooks.json');
      mkdirSync(dirname(file), { recursive: true, mode: 0o755 });
      appendFileSync(file, JSON.stringify({ ts: new Date(now()).toISOString(), message, extra }) + '\n', { mode: 0o644 });
    } catch { /* Logging must not fail the host run. */ }
  };
  const runs = new Map();
  let active = 0;
  let interval;
  const enabled = () => {
    try {
      const override = process.env.OPENCLAW_OTEL_ENABLED || process.env.TRACE_TO_GTRACE;
      if (/^(false|0|off|no)$/i.test(override || '')) return false;
      if (/^(true|1|on|yes)$/i.test(override || '')) return true;
      return JSON.parse(readFileSync(runtime.configFile, 'utf8')).enabled !== false;
    }
    catch { return false; }
  };
  const execute = (payload) => {
    if (!enabled() || active >= 4) return Promise.resolve();
    let body;
    try { body = JSON.stringify(payload); } catch { return Promise.resolve(); }
    if (Buffer.byteLength(body) > 8 * 1024 * 1024) return Promise.resolve();
    return new Promise((resolve) => {
      active++;
      let child, timer, done = false;
      const finish = () => { if (done) return; done = true; clearTimeout(timer); active--; resolve(); };
      try {
        child = launch(runtime.command, ['hook', 'openclaw'], {
          env: { ...process.env, OPENCLAW_OTEL_CONFIG_FILE: runtime.configFile },
          shell: false, windowsHide: true, stdio: ['pipe', 'ignore', 'ignore'],
        });
        child.on('error', () => { if (!done) log('bridge failed', { reason: 'spawn failed' }); finish(); });
        child.on('close', (code) => { if (!done && code !== 0) log('bridge failed', { reason: 'child exited', code }); finish(); });
        child.stdin.on('error', () => { if (!done) log('bridge failed', { reason: 'stdin failed' }); });
        timer = setTimeout(() => { log('bridge failed', { reason: 'timeout', timeout_ms: 25000 }); finish(); try { child.kill(); } catch { /* Child may already be gone. */ } }, 25000);
        child.stdin.end(body);
      } catch { log('bridge failed', { reason: 'spawn failed' }); finish(); }
    });
  };
  const keyFor = (event, ctx) => {
    const run = event.runId || ctx.runId;
    return typeof run === 'string' && run ? run : undefined;
  };
  const observe = (kind) => (event, ctx = {}) => {
    try {
      if (!enabled()) { runs.clear(); return; }
      const key = keyFor(event, ctx);
      if (!key) return;
      for (const [id, value] of runs) if (now() - value.startedAt > 3600000) runs.delete(id);
      if (!runs.has(key)) {
        if (runs.size >= 64) return;
        runs.set(key, { startedAt: now(), sessionId: event.sessionId || ctx.sessionId || ctx.sessionKey, bytes: 0, observations: [] });
      }
      const run = runs.get(key);
      if (kind === 'llm_input' && run.prompt === undefined) run.prompt = event.prompt;
      if (run.observations.length < 256) {
        // Copy the selected fields; never retain mutable host event objects or headers.
        const fields = kind === 'llm_input' ? ['provider', 'model', 'systemPrompt', 'prompt', 'historyMessages', 'imagesCount', 'tools'] :
          kind === 'llm_output' ? ['provider', 'model', 'resolvedRef', 'prompt', 'lastAssistant', 'assistantTexts', 'usage', 'reasoningEffort'] :
          kind === 'model_call_started' ? ['runId', 'callId', 'provider', 'model', 'api', 'transport'] :
          kind === 'model_call_ended' ? ['runId', 'callId', 'provider', 'model', 'api', 'transport', 'durationMs', 'outcome', 'errorCategory', 'failureKind', 'timeToFirstByteMs'] :
          kind === 'before_tool_call' ? ['toolName', 'toolKind', 'toolInputKind', 'toolCallId', 'params'] :
          ['toolName', 'toolCallId', 'params', 'result', 'error', 'durationMs'];
        const value = Object.fromEntries(fields.filter(k => event[k] !== undefined).map(k => [k, event[k]]));
        const encoded = JSON.stringify(value);
        if (encoded.length <= 131072 && run.bytes + encoded.length <= 1048576) {
          run.bytes += encoded.length;
          run.observations.push({ kind, at: now(), event: JSON.parse(encoded) });
        }
      }
    } catch { /* Observers must never fail the host run. */ }
  };
  for (const kind of ['llm_input', 'llm_output', 'model_call_started', 'model_call_ended', 'before_tool_call', 'after_tool_call']) api.on(kind, observe(kind));
  api.on('agent_end', (event, ctx = {}) => {
    try {
      const key = keyFor(event, ctx);
      const run = key && runs.get(key);
      if (key) runs.delete(key);
      return execute({ ...run, event: 'agent_end', at: now(), runId: event.runId || ctx.runId,
        sessionId: ctx.sessionId || run?.sessionId || ctx.sessionKey, trigger: ctx.trigger,
        success: event.success, error: event.error, durationMs: event.durationMs,
        messages: event.messages });
    } catch { return Promise.resolve(); }
  });
  api.registerService({ id: 'obs-agent-connector', start() {
      clearInterval(interval);
      interval = setInterval(() => { void execute({ event: 'flush' }); }, 60000);
      interval.unref?.();
      return execute({ event: 'flush' });
    },
    stop() { clearInterval(interval); runs.clear(); return execute({ event: 'flush' }); } });
}

export default { id: 'obs-agent-connector', name: 'OBS Agent Connector',
  description: 'Built-in Go telemetry adapter', register: registerBridge };
