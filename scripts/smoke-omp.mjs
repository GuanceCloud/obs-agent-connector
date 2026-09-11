// Exercise the installed OMP binary with a local synthetic model and collector.
// Usage: node scripts/smoke-omp.mjs /absolute/connector /absolute/omp
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname, resolve } from "node:path";
import { gunzipSync } from "node:zlib";

const parallelMode = process.argv.includes("--parallel");
const nestedMode = process.argv.includes("--nested");
const subagentMode = parallelMode || nestedMode || process.argv.includes("--subagent");
const connector = resolve(process.argv[2]);
const omp = resolve(process.argv[3]);
const root = mkdtempSync(join(tmpdir(), "connector-omp-smoke-"));
const agentDir = join(root, ".omp", "agent");
const configFile = join(root, ".obs-agent-connector", "omp", "gtrace.json");
const env = { HOME: root, USERPROFILE: root, PI_CODING_AGENT_DIR: agentDir,
  PATH: dirname(omp) + ":" + process.env.PATH, TMPDIR: tmpdir(), NO_PROXY: "127.0.0.1,localhost" };
const uploads = [];
let requests = 0;
const server = createServer(async (req, res) => {
  const chunks = [];for await (const chunk of req) chunks.push(chunk);
  const body = Buffer.concat(chunks);
  if (req.url.startsWith("/v1/write/")) {
    assert.equal(req.headers["x-token"], "synthetic-token");
    assert.equal(req.headers["content-type"], "application/x-protobuf");
    const decoded = req.headers["content-encoding"] === "gzip" ? gunzipSync(body) : body;
    uploads.push({ path: req.url, body: decoded });res.end("{}");return;
  }
  if (req.url.endsWith("/chat/completions")) {
    const input = JSON.parse(body);
    requests++;
    if (parallelMode && input.messages.some(m=>m.role==="tool") && !input.tools?.some(t=>t.function?.name==="yield")) await new Promise(resolve=>setTimeout(resolve,1500));
    const hasToolResult = input.messages.some(m => m.role === "tool");
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    const emit = (delta, finish = null, usage) => res.write("data: " + JSON.stringify({ id: "synthetic-" + requests, object: "chat.completion.chunk", created: 1700000000, model: "mock", choices: [{ index: 0, delta, finish_reason: finish }], ...(usage ? { usage } : {}) }) + "\n\n");
    emit({ role: "assistant" });
    if (subagentMode && input.tools?.some(t => t.function?.name === "yield")) {
      if (nestedMode && !hasToolResult && !input.messages.filter(m=>m.role==="user").some(m=>JSON.stringify(m.content).includes("SYNTHETIC_GRANDCHILD"))) {
        emit({tool_calls:[{index:0,id:"synthetic-nested",type:"function",function:{name:"task",arguments:JSON.stringify({agent:"telemetry-leaf",task:"SYNTHETIC_GRANDCHILD: Return a synthetic result using yield."})}}]});
      } else emit({tool_calls:[{index:0,id:"synthetic-yield",type:"function",function:{name:"yield",arguments:JSON.stringify({data:"Synthetic child result"})}}]});
      emit({}, "tool_calls", {prompt_tokens:8,completion_tokens:4,total_tokens:12});
    } else if (subagentMode && !hasToolResult) {
      emit({tool_calls:[{index:0,id:"synthetic-task|fc_native",type:"function",function:{name:"task",arguments:JSON.stringify(parallelMode ? {context:"Synthetic parallel telemetry validation",tasks:[{name:"SmokeChildA",agent:"telemetry-test",task:"Return a synthetic result using yield."},{name:"SmokeChildB",agent:"telemetry-test",task:"Return a synthetic result using yield."}]} : {agent:"telemetry-test",task:"Return a synthetic result using yield."})}}]});
      emit({}, "tool_calls", {prompt_tokens:10,completion_tokens:3,total_tokens:13});
    } else if (!hasToolResult) {
      emit({ tool_calls: [{ index: 0, id: "synthetic-read", type: "function", function: { name: "read", arguments: JSON.stringify({ path: join(root, "hello.txt") }) } }] });
      emit({}, "tool_calls", { prompt_tokens: 10, completion_tokens: 3, total_tokens: 13 });
    } else {
      emit({ content: "Hello from the synthetic model." });
      emit({}, "stop", { prompt_tokens: 15, completion_tokens: 5, total_tokens: 20 });
    }
    res.end("data: [DONE]\n\n");return;
  }
  res.writeHead(404);res.end("{}");
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const endpoint = `http://127.0.0.1:${server.address().port}`;
const command = (args) => {
  const result = spawnSync(connector, args, { cwd: root, env, encoding: "utf8", timeout: 15000 });
  assert.equal(result.status, 0, result.stdout + result.stderr);return result.stdout;
};
try {
  mkdirSync(agentDir, { recursive: true });
  if (subagentMode) {
    mkdirSync(join(agentDir,"agents"),{recursive:true});
    if(parallelMode) writeFileSync(join(agentDir,"config.yml"),"task:\n  batch: true\nasync:\n  enabled: true\n");
    writeFileSync(join(agentDir,"agents","telemetry-test.md"),"---\nname: telemetry-test\ndescription: Synthetic telemetry validation\nmodel: connector-test/mock\ntools: read, task\nblocking: true\nprewalk: false\nadvisor: false\n---\nReturn a synthetic result using yield.\n".replace("blocking: true",parallelMode ? "blocking: false" : "blocking: true"));
    writeFileSync(join(agentDir,"agents","telemetry-leaf.md"),"---\nname: telemetry-leaf\ndescription: Synthetic nested telemetry validation\nmodel: connector-test/mock\ntools: read\nblocking: true\nprewalk: false\nadvisor: false\n---\nReturn a synthetic result using yield.\n".replace("blocking: true",parallelMode ? "blocking: false" : "blocking: true"));
  }
  writeFileSync(join(root, "hello.txt"), "Synthetic test content.\n");
  writeFileSync(join(agentDir, "models.yml"), JSON.stringify({ providers: { "connector-test": {
    baseUrl: endpoint + "/v1", api: "openai-completions", apiKey: "synthetic-model-key",
    models: [{ id: "mock", name: "Mock", reasoning: false, input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 1024 }],
  } } }));
  command(["install", "omp", "--endpoint", endpoint, "--x-token", "synthetic-token", "--yes"]);
  assert(command(["status", "omp"]).includes("true"));
  const before = readFileSync(configFile, "utf8");
  command(["update", "omp", "--yes"]);
  assert.equal(readFileSync(configFile, "utf8"), before);
  const args = ["-p", "--mode", "json", "--model", "connector-test/mock", "--tools", subagentMode ? "read,task" : "read", "--no-lsp", "--no-skills", "--no-rules", "--no-title", "Read hello.txt and say hello."];
  const child = spawn(omp, args, { cwd: root, env, stdio: ["ignore", "pipe", "pipe"] });
  let output = "";child.stdout.on("data", c => output += c);child.stderr.on("data", c => output += c);
  const timer = setTimeout(() => child.kill("SIGKILL"), 45000);
  const code = await new Promise(resolve => child.on("close", resolve));clearTimeout(timer);
  assert.equal(code, 0, output.slice(-5000));
  for (let i = 0; i < 100 && uploads.length < ((nestedMode || parallelMode) ? 6 : subagentMode ? 4 : 2); i++) await new Promise(resolve => setTimeout(resolve, 100));
  assert.equal(uploads.length, (nestedMode || parallelMode) ? 6 : subagentMode ? 4 : 2, "Expected traces and metrics. OMP output:\n" + output.slice(-3000));
  assert(requests >= 2, "model did not run tool round trip");
  const traces = uploads.filter(u => u.path.endsWith("otel-llm")).map(u=>u.body.toString()).join("\n");
  assert(traces.includes("invoke_agent") && traces.includes(subagentMode ? "tool:task" : "tool:read") && traces.includes("llm"));
  if (subagentMode) {
    const spans=uploads.filter(u=>u.path.endsWith("otel-llm")).flatMap(u=>traceSpans(u.body));
    const ids=new Set(spans.map(s=>s.id));
    assert.equal(ids.size,spans.length,"duplicate span IDs");
    assert.equal(new Set(spans.map(s=>s.trace)).size,1,"child trace disconnected");
    const rootSpan=spans.find(s=>s.name==="invoke_agent" && !s.parent);
    const task=spans.find(s=>s.name==="tool:task" && s.parent===rootSpan.id);
    const childRoot=spans.find(s=>s.name==="invoke_agent" && s.parent===task?.id);
    assert(childRoot,"child root not attached to native delegate tool");
    assert(spans.some(s=>s.name==="llm" && s.parent===childRoot.id),"child LLM missing");
    if(parallelMode) assert.equal(spans.filter(s=>s.name==="invoke_agent" && s.parent===task.id).length,2,"parallel child missing");
    if(nestedMode) { const nested=spans.find(s=>s.name==="tool:task" && s.parent===childRoot.id); assert(nested && spans.some(s=>s.name==="invoke_agent" && s.parent===nested.id),"nested child disconnected"); }
    assert(spans.every(s=>s.id!==s.parent),"self-parent span");
  }
  command(["disable", "omp"]);assert.equal(JSON.parse(readFileSync(configFile)).enabled, false);
  command(["enable", "omp"]);assert.equal(JSON.parse(readFileSync(configFile)).enabled, true);
  command(["remove", "omp", "--yes"]);
  console.log(`OMP product smoke passed: ${requests} local model calls, Trace + Metrics, install/update/status/toggle/remove.`);
} finally {
  server.closeAllConnections();await new Promise(resolve => server.close(resolve));
  rmSync(root, { recursive: true, force: true });
}

// Decode only the standard OTLP envelope and span identity fields for this smoke.
function fields(buffer) {
  let p=0; const result=[];
  const varint=()=>{let value=0,shift=0,b; do {b=buffer[p++];value+=(b&127)*2**shift;shift+=7;} while(b&128);return value;};
  while(p<buffer.length) {
    const tag=varint(),field=Math.floor(tag/8),wire=tag%8;
    if(wire===2) {const n=varint();result.push([field,buffer.subarray(p,p+n)]);p+=n;}
    else if(wire===0) varint(); else if(wire===1) p+=8; else if(wire===5) p+=4; else throw Error("unsupported protobuf wire");
  }
  return result;
}
function traceSpans(buffer) {
  const get=(b,n)=>fields(b).filter(([f])=>f===n).map(([,v])=>v);
  return get(buffer,1).flatMap(r=>get(r,2)).flatMap(s=>get(s,2)).map(s=>({
    trace:get(s,1)[0]?.toString("hex"),id:get(s,2)[0]?.toString("hex"),parent:get(s,4)[0]?.toString("hex"),name:get(s,5)[0]?.toString()
  }));
}
