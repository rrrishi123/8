// Minimal BiDi broker — the "channel" the 8 collector connects to.
// Bridges Firefox's WebDriver-BiDi WebSocket <-> the collector's HTTP contract:
//   GET  /events   -> SSE stream, one `data: <bidi-frame-json>` per WS message
//   POST /command  -> {method, params} sent over BiDi, the matching reply returned
//
// v0.0.1 review: the broker now echoes every command into /events as a __cmd frame
// (origin=wire) so the witness sees efferent traffic automatically — no separate
// /run path needed. An agent using http-mcp raw is fully visible on 8. The witness
// filters its own traffic by tagging its afferent polling origin=witness.
//
// Env: BIDI_WS (ws://127.0.0.1:9222/session/<id>), PORT (default 4445).
import http from 'node:http';

const WS_URL = process.env.BIDI_WS;
const PORT = parseInt(process.env.PORT || '4445', 10);

let ws, nextId = 1;
const pending = new Map();      // bidi id -> resolve
const subscribers = new Set();  // SSE response objects

function fanout(msg) {
  const line = `data: ${JSON.stringify(msg)}\n\n`;
  for (const res of subscribers) { try { res.write(line); } catch {} }
}

function connect() {
  ws = new WebSocket(WS_URL);
  ws.addEventListener('open', () => {
    console.log('[broker] BiDi connected ->', WS_URL);
    // subscribe to the modules the cockpit cares about
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

const server = http.createServer(async (req, res) => {
  if (req.method === 'GET' && req.url === '/events') {
    res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', 'Connection': 'keep-alive' });
    res.write(': broker events open\n\n');
    subscribers.add(res);
    req.on('close', () => subscribers.delete(res));
    return;
  }
  if (req.method === 'POST' && req.url === '/command') {
    let body = ''; for await (const c of req) body += c;
    let cmd; try { cmd = JSON.parse(body); } catch { res.writeHead(400); return res.end('{"error":"bad json"}'); }
    const id = nextId++; // snapshot before send — the echo carries the matched id
    const reply = await sendRaw(cmd.method, cmd.params);
    // __cmd echo: fan the efferent command into /events so the witness
    // sees every command on the wire, not only its own /run path.
    fanout({ __cmd: true, origin: 'wire', id, method: cmd.method, params: cmd.params || {} });
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(reply));
    return;
  }
  res.writeHead(404); res.end();
});

connect();
server.listen(PORT, '127.0.0.1', () => console.log(`[broker] http :${PORT} bridging -> ${WS_URL}`));
