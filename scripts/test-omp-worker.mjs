// Verify the JS handoff lifecycle without contacting a telemetry endpoint.
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { mkdtempSync, writeFileSync, readFileSync, readdirSync, existsSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = mkdtempSync(join(tmpdir(), "omp-worker-test-"));
const originalError = console.error;
const logs = [];
console.error = line => logs.push(line);
try {
  const cfg = join(root, "gtrace.json");
  writeFileSync(cfg, JSON.stringify({enabled:true}));
  const source = readFileSync(new URL("../internal/adapters/omp/bridge/extension.js", import.meta.url), "utf8")
    .replace('import { spawn } from "node:child_process";', 'const spawn = (...args) => globalThis.ompTestSpawn(...args);')
    .replaceAll("__CONNECTOR_EXECUTABLE__", JSON.stringify("fake-worker"))
    .replaceAll("__CONNECTOR_CONFIG__", JSON.stringify(cfg))
    .replace("}, 35000)", "}, 20)");
  const file = join(root, "extension.mjs");
  writeFileSync(file, source);
  const {default:register} = await import(pathToFileURL(file));
  for (const outcome of ["success", "spawn", "throw", "crash", "timeout"]) {
    let child, snapshot;
    globalThis.ompTestSpawn = (executable, args) => {
      assert.equal(executable, "fake-worker");
      assert.equal(args.at(-2), "--snapshot");
      snapshot = args.at(-1);
      assert(existsSync(snapshot));
      if (outcome === "throw") throw Object.assign(new Error("private details"), {code:"ENOENT"});
      child = new EventEmitter();
      child.unref = () => {};
      child.kill = signal => { assert.equal(signal, "SIGKILL"); assert(existsSync(snapshot)); child.emit("exit", null, signal); };
      return child;
    };
    const handlers = new Map();
    register({on:(name, fn) => handlers.set(name, fn)});
    const ctx = {sessionManager:{getSessionId:()=>outcome}};
    handlers.get("message_start")({message:{role:"user",content:"hello"}}, ctx);
    handlers.get("agent_end")({}, ctx);
    if (outcome === "spawn") child.emit("error", {code:"ENOENT"});
    if (outcome === "success") child.emit("exit", 0, null);
    if (outcome === "crash") child.emit("exit", 1, null);
    if (outcome === "timeout") await new Promise(resolve => setTimeout(resolve, 40));
    assert(!existsSync(snapshot), `${outcome} retained snapshot`);
  }
  assert(logs.some(line => line.includes("stage=spawn code=ENOENT")));
  assert(logs.some(line => line.includes("stage=timeout code=WORKER_TIMEOUT")));
  assert(!logs.some(line => line.includes("private details")));
  assert.deepEqual(readdirSync(join(root,"state","queue")), []);
} finally {
  console.error = originalError;
  delete globalThis.ompTestSpawn;
  rmSync(root, {recursive:true,force:true});
}
console.log("OMP worker tests passed: single-file handoff, spawn failure, exit, crash and timeout cleanup.");
