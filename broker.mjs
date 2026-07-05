// BiDi broker — the "channel" the 8 collector connects to, now with a TAB-LEASE
// REGISTRY so many AI agents (pilot instances, DeepSeek-backed claude-ds, this
// Claude, any local AI) share the ONE Firefox at once, each with the illusion of
// its own session. Designed three-way (claude + pilot + deepseek-web, 2026-07-05).
//
//   GET  /events[?context=CTX] -> SSE; unfiltered = all frames (witness/cockpit),
//                                 ?context= = only that tab's frames (per-agent demux)
//   POST /command  -> {method, params} sent over BiDi, matching reply returned.
//        Registry ops (JSON body):
//          {"claim":"agent-id"}     -> create/reuse a tab, return {context}
//          {"release":"agent-id"}   -> close that tab, drop the lease
//          {"heartbeat":"agent-id"} -> refresh lease, return {status,context}
//        Standard BiDi with {"agent":"id",...}: the agent's leased context is
//        injected when absent, so no one falls back to contexts[0] (collision C1).
//   GET  /leases   -> the registry (read-only; pilot's heartbeat loop + witness)
//   GET  /ws       -> {ws} the LIVE BiDi endpoint, so agents re-point after a
//                     recycle instead of hardcoding (collision C4).
//
// SHARED-SESSION GUARDS (collisions C2/C3): session.new / session.end /
// browser.close are refused — one agent must never kill the session for everyone.
// "End my session" = {release} (closes only your tab). The registry is the file
// ~/.8/leases.json (pilot owns reading it + the N=3 liveness policy; lease is
// OWNERSHIP, liveness is capture — orthogonal, per deepseek-web).
//
// Env: BIDI_WS (ws://127.0.0.1:9222/session/<id>), PORT (default 4445).
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';

const WS_URL = process.env.BIDI_WS;
const PORT = parseInt(process.env.PORT || '4445', 10);
const REG = path.join(os.homedir(), '.8', 'leases.json');

let ws, nextId = 1;
const pending = new Map();      // bidi id -> resolve
const subscribers = new Set();  // { res, ctx } — ctx is optional per-agent filter

const now = () => new Date().toISOString();
function readReg() { try { return JSON.parse(fs.readFileSync(REG, 'utf8')); } catch { return {}; } }
function writeReg(r) { try { fs.mkdirSync(path.dirname(REG), { recursive: true }); fs.writeFileSync(REG, JSON.stringify(r, null, 2)); } catch (e) { console.log('[broker] reg write failed:', e?.message); } }

// A frame's context, if any (browsingContext.* events carry params.context;
// script/log events carry params.source.context). Session-level frames have none.
function frameCtx(msg) { return msg?.params?.context || msg?.params?.source?.context || null; }

function fanout(msg) {
  const fc = frameCtx(msg);
  const line = `data: ${JSON.stringify(msg)}\n\n`;
  for (const s of subscribers) {
    // unfiltered subscriber sees everything; filtered one sees its own tab's
    // frames + session-level (context-less) frames. That's the per-agent demux.
    if (!s.ctx || !fc || s.ctx === fc) { try { s.res.write(line); } catch {} }
  }
}

function connect() {
  ws = new WebSocket(WS_URL);
  ws.addEventListener('open', () => {
    console.log('[broker] BiDi connected ->', WS_URL);
    sendRaw('session.subscribe', { events: ['browsingContext', 'log', 'network', 'script'] });
  });
  ws.addEventListener('message', (ev) => {
    let msg; try { msg = JSON.parse(ev.data); } catch { return; }
    if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id); }
    fanout(msg);
  });
  ws.addEventListener('close', () => { console.log('[broker] BiDi closed; reconnecting in 1s'); setTimeout(connect, 1000); });
  ws.addEventListener('error', (e) => console.log('[broker] ws error:', e?.message || e));
}

function sendRaw(method, params) {
  return new Promise((resolve) => {
    const id = nextId++;
    pending.set(id, resolve);
    try { ws.send(JSON.stringify({ id, method, params: params || {} })); }
    catch (e) { pending.delete(id); resolve({ id, type: 'error', error: String(e) }); return; }
    setTimeout(() => { if (pending.has(id)) { pending.delete(id); resolve({ id, type: 'error', error: 'timeout' }); } }, 30000);
  });
}

async function newTab() { const r = await sendRaw('browsingContext.create', { type: 'tab' }); return r?.result?.context || null; }

// claim: give the agent a tab. Idempotent — reuse the live lease if its tab still
// exists, else mint a fresh one. (No getTree per call; a stale context surfaces
// as a command error and is reclaimed lazily — see runForAgent.)
async function claim(agent) {
  const r = readReg(); const cur = r[agent];
  if (cur && cur.context && cur.status !== 'released') { cur.heartbeat_at = now(); cur.status = 'active'; writeReg(r); return { context: cur.context, reused: true }; }
  const ctx = await newTab(); if (!ctx) return { error: 'browsingContext.create failed' };
  r[agent] = { context: ctx, claimed_at: now(), heartbeat_at: now(), status: 'active' };
  writeReg(r); return { context: ctx };
}
async function release(agent) {
  const r = readReg(); const cur = r[agent];
  if (cur?.context) await sendRaw('browsingContext.close', { context: cur.context });
  delete r[agent]; writeReg(r); return { ok: true };
}
function heartbeat(agent) {
  const r = readReg(); if (!r[agent]) return { error: 'no lease — claim first' };
  r[agent].heartbeat_at = now(); r[agent].status = 'active'; writeReg(r);
  return { status: 'active', context: r[agent].context };
}

const CTX_METHODS = /^browsingContext\.(navigate|close|reload|activate|captureScreenshot|print|setViewport|handleUserPrompt|traverseHistory|locateNodes)$/;
const SCRIPT_METHODS = /^script\.(evaluate|callFunction)$/;
const FORBIDDEN = new Set(['session.new', 'session.end', 'browser.close']); // would kill the shared session for everyone

// runForAgent: inject the agent's leased context where the method needs one, send,
// and if BiDi reports the context is gone (tab was closed/frozen), reclaim a fresh
// tab, update the lease, and retry once. Any command counts as a heartbeat.
async function runForAgent(agent, method, params) {
  params = params || {};
  const r = readReg();
  if (!r[agent]) { await claim(agent); }
  const reg = readReg(); let ctx = reg[agent]?.context;
  const inject = (p) => {
    if (!ctx) return p;
    if (CTX_METHODS.test(method) && p.context == null) p.context = ctx;
    else if (SCRIPT_METHODS.test(method) && (p.target == null || p.target.context == null)) p.target = { context: ctx, ...(p.target || {}) };
    else if (/^input\./.test(method) && p.context == null) p.context = ctx;
    return p;
  };
  let reply = await sendRaw(method, inject({ ...params }));
  const errTxt = JSON.stringify(reply?.error || reply?.message || '');
  if (/no such frame|no such (browsing )?context|unknown context|not found/i.test(errTxt)) {
    const fresh = await newTab();
    if (fresh) { const r2 = readReg(); if (r2[agent]) { r2[agent].context = fresh; r2[agent].heartbeat_at = now(); r2[agent].status = 'active'; writeReg(r2); } ctx = fresh; reply = await sendRaw(method, inject({ ...params })); }
  } else { const r3 = readReg(); if (r3[agent]) { r3[agent].heartbeat_at = now(); writeReg(r3); } }
  return reply;
}

const server = http.createServer(async (req, res) => {
  const json = (code, obj) => { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(obj)); };

  if (req.method === 'GET' && req.url.startsWith('/events')) {
    const u = new URL(req.url, 'http://x'); const ctx = u.searchParams.get('context') || null;
    res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', 'Connection': 'keep-alive' });
    res.write(': broker events open\n\n');
    const sub = { res, ctx }; subscribers.add(sub);
    req.on('close', () => subscribers.delete(sub));
    return;
  }
  if (req.method === 'GET' && req.url === '/leases') return json(200, readReg());
  if (req.method === 'GET' && req.url === '/ws') return json(200, { ws: WS_URL });
  // /health — discover (http-mcp heldChannels) probes this to find the broker;
  // it also carries the invariants every agent must know, so the rules flow to
  // any local AI on connect (share this socket; claim your tab; never a 2nd ws;
  // never session.end/DeleteSession; Firefox is XWayland — never native-Wayland).
  if (req.method === 'GET' && req.url === '/health') {
    const reg = readReg();
    return json(200, {
      ok: true, ws: WS_URL, agents: Object.keys(reg).length,
      session_illusion: 'One Firefox, one BiDi session, many tabs. Your private session = a leased tab. POST {"claim":"<agent-id>"} to get your context, then send {"agent":"<id>",method,params} and the broker injects your tab. {"heartbeat":"<id>"} to keep it, {"release":"<id>"} to close it.',
      invariants: [
        'Firefox runs under XWayland (MOZ_ENABLE_WAYLAND=0) — NEVER relaunch it native-Wayland; that crashes Hyprland (safe-mode).',
        'ONE shared socket at :4445 — never open a 2nd websocket / session.new (refused).',
        'NEVER session.end / DeleteSession — it kills the shared session for every agent (refused here). To end, {"release":"<agent-id>"}.',
        'Claim your own tab; do not drive contexts[0] blind — pass {"agent":"<id>"} and the broker leases+injects your context.'
      ],
    });
  }

  if (req.method === 'POST' && req.url === '/command') {
    let body = ''; for await (const c of req) body += c;
    let cmd; try { cmd = JSON.parse(body); } catch { return json(400, { error: 'bad json' }); }

    // ---- registry ops ----
    if (cmd.claim) return json(200, await claim(cmd.claim));
    if (cmd.release) return json(200, await release(cmd.release));
    if (cmd.heartbeat) return json(200, heartbeat(cmd.heartbeat));

    // ---- shared-session guards (any caller, agent or not) ----
    if (FORBIDDEN.has(cmd.method)) return json(200, { type: 'error', error: 'forbidden', message: `${cmd.method} would kill the shared Firefox session for every agent. To end your session, POST {"release":"<agent-id>"} — it closes only your tab.` });

    // ---- standard BiDi ----
    const id = nextId++;
    const reply = cmd.agent ? await runForAgent(cmd.agent, cmd.method, cmd.params) : await sendRaw(cmd.method, cmd.params);
    fanout({ __cmd: true, origin: 'wire', agent: cmd.agent || null, id, method: cmd.method, params: cmd.params || {} });
    return json(200, reply);
  }
  res.writeHead(404); res.end();
});

connect();
server.listen(PORT, '127.0.0.1', () => console.log(`[broker] http :${PORT} bridging -> ${WS_URL} (lease registry: ${REG})`));
