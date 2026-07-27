// serve.mjs — the demo witness. One zero-dep Node server that (a) serves the
// experience/ static site and (b) IS a minimal 8: /health (CALL), /feed (SSE
// witness stream), /fire (record who-fired-what and broadcast it). This makes
// the whole demo self-contained — no home, no omarchy, no build. Fire any
// transport page and it lands in the witness wall in real time.
//
// Run:  node experience/serve.mjs   (PORT env optional, default 8100)
// The same shape ports to a Cloudflare Worker (Durable Object holds `clients`)
// for an always-up, home-decoupled public demo.
import http from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, join, normalize } from 'node:path';
import { fileURLToPath } from 'node:url';

const DIR = fileURLToPath(new URL('.', import.meta.url));
const PORT = process.env.PORT || 8100;
const START = Date.now();
const clients = new Set();          // open SSE responses = the witnesses
let fireCount = 0;

const MIME = { '.html':'text/html', '.css':'text/css', '.js':'text/javascript',
  '.mjs':'text/javascript', '.json':'application/json', '.svg':'image/svg+xml',
  '.png':'image/png', '.ico':'image/x-icon' };

const cors = res => {
  res.setHeader('Access-Control-Allow-Origin', '*');
  res.setHeader('Access-Control-Allow-Methods', 'GET,POST,OPTIONS');
  res.setHeader('Access-Control-Allow-Headers', 'Content-Type');
};

function broadcast(obj){
  const line = `data: ${JSON.stringify(obj)}\n\n`;
  for(const c of clients){ try{ c.write(line); }catch{} }
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`);
  cors(res);
  if(req.method === 'OPTIONS'){ res.writeHead(204); return res.end(); }

  // --- the witness endpoints ---
  if(url.pathname === '/health'){
    res.writeHead(200, {'Content-Type':'application/json'});
    return res.end(JSON.stringify({ alive:true, role:'demo-witness',
      watchers:clients.size, fires:fireCount, uptime_s:Math.round((Date.now()-START)/1000) }));
  }

  if(url.pathname === '/feed'){                       // afferent stream (SSE)
    res.writeHead(200, {'Content-Type':'text/event-stream','Cache-Control':'no-cache','Connection':'keep-alive'});
    res.write(': witness feed open\n\n');
    clients.add(res);
    const hb = setInterval(() => { try{ res.write(`event: tick\ndata: {"t":${Date.now()}}\n\n`); }catch{} }, 3000);
    req.on('close', () => { clearInterval(hb); clients.delete(res); });
    return;
  }

  if(url.pathname === '/fire' && req.method === 'POST'){   // a host fired — catch it
    let body = ''; for await (const ch of req) body += ch;
    let p = {}; try{ p = JSON.parse(body || '{}'); }catch{}
    const seen = Date.now();
    const fired = Number(p.t) || seen;
    const rec = { kind:'fire', seq:++fireCount, who:p.who||'?', what:p.what||'?',
      mode:p.mode||'', fired, seen, dms:Math.max(0, seen-fired) };
    broadcast(rec);
    res.writeHead(200, {'Content-Type':'application/json'});
    return res.end(JSON.stringify({ ok:true, seq:rec.seq, seen_ms:rec.dms }));
  }

  // --- static files (experience/) ---
  let p = decodeURIComponent(url.pathname);
  if(p === '/') p = '/index.html';
  const file = normalize(join(DIR, p));
  if(!file.startsWith(DIR)){ res.writeHead(403); return res.end('no'); }
  try{
    const data = await readFile(file);
    res.writeHead(200, {'Content-Type': MIME[extname(file)] || 'application/octet-stream'});
    res.end(data);
  }catch{
    res.writeHead(404, {'Content-Type':'text/plain'}); res.end('404');
  }
});

server.listen(PORT, () =>
  console.log(`8 demo-witness · http://localhost:${PORT} · serves experience/ + /health /feed /fire`));
