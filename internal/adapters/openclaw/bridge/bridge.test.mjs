import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, copyFileSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { EventEmitter } from 'node:events';

test('native bridge isolates runs, passes argv without a shell, and respects disable', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'openclaw-bridge-'));
  try {
    copyFileSync(new URL('./index.mjs', import.meta.url), join(dir, 'index.mjs'));
    const configFile = join(dir, 'gtrace.json');
    writeFileSync(configFile, '{"enabled":true}');
    const command = join(dir, 'connector with spaces');
    writeFileSync(join(dir, 'runtime.json'), JSON.stringify({ command, configFile }));
    const { registerBridge } = await import(pathToFileURL(join(dir, 'index.mjs')));
    const hooks = new Map(), sent = [];
    let service;
    const launch = (exe, args, options) => {
      assert.equal(exe, command);
      assert.deepEqual(args, ['hook', 'openclaw']);
      assert.equal(options.shell, false);
      const child = new EventEmitter();
      child.stdin = new EventEmitter();
      child.stdin.end = body => { sent.push(JSON.parse(body)); queueMicrotask(() => child.emit('close', 0)); };
      child.kill = () => child.emit('close');
      return child;
    };
    registerBridge({ on: (name, cb) => hooks.set(name, cb), registerService: s => { service = s; } }, launch, () => 2000);
    hooks.get('llm_input')({ runId: 'a', sessionId: 's1', prompt: 'first' }, {});
    hooks.get('llm_input')({ runId: 'b', sessionId: 's2', prompt: 'second' }, {});
    hooks.get('llm_output')({ runId: 'a', sessionId: 's1', assistantTexts: ['done'], usage: { input: 3 } }, {});
    hooks.get('model_call_ended')({runId:'a',callId:'call-1',provider:'test',model:'small',durationMs:500,timeToFirstByteMs:125,outcome:'completed',headers:{Authorization:'private'}},{});
    await hooks.get('agent_end')({ runId: 'a', success: true, messages: [] }, { sessionKey: 'alias-s1' });
    assert.equal(sent[0].sessionId, 's1');
    assert.equal(sent[0].prompt, 'first');
    assert.equal(sent[0].observations[2].event.timeToFirstByteMs,125);
    assert.equal(sent[0].observations[2].event.headers,undefined);
    assert.equal(sent[0].observations.length, 3);
    await hooks.get('agent_end')({ runId: 'b', success: true, messages: [] }, {});
    assert.equal(sent[1].prompt, 'second');
    assert.equal(sent[1].observations.length, 1);
    writeFileSync(configFile, '{"enabled":false}');
    await hooks.get('agent_end')({ runId: 'c', success: true }, {});
    assert.equal(sent.length, 2);
    await service.stop();
    writeFileSync(configFile, '{"enabled":true}');
    registerBridge({ on: (name, cb) => hooks.set(name, cb), registerService: () => {} }, () => { throw new Error('spawn failed'); });
    await hooks.get('agent_end')({ runId: 'd', success: true }, { sessionId: 's' });
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
