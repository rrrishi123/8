import { useEffect, useRef, useState } from 'react';
import { fetchUrl, run, shot, listSessions } from '../lib/api';
import type { Session } from '../types';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

// The WIRE pane, two-wire style: every transport is a LIVE WIRE you can see —
// a held CHANNEL glows and carries pushed events; a CALL lights only for one
// round trip then goes dark ("request, response, gone"). Firing a row is a REAL
// op (browser fetch/ws/shot, or the adapters `loopback` via /adapters/fire) and
// the packets you watch are timed by the real response. Not a diagram of the
// physics — the physics, witnessed. Animation runs on setInterval (not rAF) so
// an AGENT filming through captureScreenshot sees it move even when the window
// is occluded (the canvas-view rAF lesson, 2026-07-27).

type Dir = 'out' | 'in';
interface Packet { p: number; dir: Dir; color: string; label: string; speed: number }
// an arrival pulse: the node ABSORBS the packet (a ring that flashes and fades)
// — without it, dots overshot past both endpoints and blinked out mid-air
// (the A/B "viewing problem": travel was allowed to p=1.06, 7% past the node).
interface Pulse { dir: Dir; color: string; t0: number }
interface WireState {
  held: boolean;        // a held channel: line stays lit, events may drift in
  packets: Packet[];
  pulses: Pulse[];
  midNode?: string;     // mqtt: the broker sits ON the wire
  rightLabel: string;
}

interface Row {
  name: string;
  atom: string;
  phys: 'call' | 'channel' | 'afferent' | 'both';
  where: 'wire' | 'adapter';
  what: string;
  use: string;
  right: string;        // the far-end node label
  mid?: string;
  fire: (st: WireState, spawn: (dir: Dir, color: string, label: string) => void, chan: string | null) => Promise<string>;
}

const css = (v: string) => getComputedStyle(document.documentElement).getPropertyValue(v).trim() || '#888';

export function Transports() {
  const [chan, setChan] = useState<string | null>(null);
  const [res, setRes] = useState<Record<string, { ok: boolean; text: string } | 'firing'>>({});
  const canvases = useRef<Record<string, HTMLCanvasElement | null>>({});
  const states = useRef<Record<string, WireState>>({});

  useEffect(() => {
    const load = () => listSessions()
      .then((ss: Session[]) => setChan(ss.find((s) => s.physics === 'channel' && s.status !== 'disconnected')?.id || null))
      .catch(() => {});
    load();
    const t = window.setInterval(load, 4000);
    return () => clearInterval(t);
  }, []);

  const ROWS: Row[] = [
    {
      name: 'http', atom: 'CALL', phys: 'call', where: 'wire',
      what: 'one request, one response, then nothing', use: 'every WebDriver / Appium / REST hub command', right: 'server',
      fire: async (st, spawn) => {
        spawn('out', css('--blue'), 'req');
        const r = await fetchUrl({ method: 'GET', url: 'https://api.github.com/zen', headers: {}, body: '' });
        spawn('in', css('--blue'), 'res');
        return `${r.status} · ${r.latency_ms}ms · “${(r.body || '').slice(0, 42)}”`;
      },
    },
    {
      name: 'websocket', atom: 'CHANNEL', phys: 'channel', where: 'wire',
      what: 'one held bidirectional socket — cmd ⇄ reply, matched by id', use: 'CDP / BiDi: every Playwright test IS this', right: 'browser',
      fire: async (st, spawn, chan) => {
        if (!chan) throw new Error('no channel session held');
        spawn('out', css('--purple'), 'cmd');
        const r = await run(chan, 'browsingContext.getTree', {}) as { result?: { contexts?: unknown[] } };
        spawn('in', css('--purple'), 'reply');
        return `getTree → ${r?.result?.contexts?.length ?? '?'} tabs on the held socket`;
      },
    },
    {
      name: 'sse', atom: 'CHANNEL · afferent', phys: 'afferent', where: 'wire',
      what: 'server pushes, client only listens', use: 'the feed this cockpit runs on', right: '8 feed',
      fire: (st, spawn) => new Promise((resolve, reject) => {
        const es = new EventSource(`${BASE}/feed`);
        let n = 0;
        const to = setTimeout(() => { es.close(); n ? resolve(`${n} events pushed (closed early)`) : reject(new Error('no events in 6s')); }, 6000);
        es.onmessage = () => { n++; spawn('in', css('--yellow'), 'evt'); if (n >= 3) { clearTimeout(to); es.close(); resolve('3 real events pushed by the server — never requested'); } };
        es.onerror = () => { clearTimeout(to); es.close(); reject(new Error('feed unreachable')); };
      }),
    },
    {
      name: 'mjpeg', atom: 'CHANNEL · afferent', phys: 'afferent', where: 'wire',
      what: 'multipart/x-mixed-replace — the media plane', use: 'live device / browser screencast', right: 'screen',
      fire: async (st, spawn, chan) => {
        if (!chan) throw new Error('no channel session held');
        const data = await shot(chan);
        spawn('in', css('--yellow'), 'frame');
        return `captured a real frame · ${Math.round((data?.length || 0) / 1024)}kb of pixels`;
      },
    },
    {
      name: 'unix', atom: 'CALL | CHANNEL', phys: 'call', where: 'wire',
      what: 'the same bytes over a unix-domain socket — no TCP port', use: 'portless daemons: dockerd, driver sidecars', right: '∅ socket',
      fire: async (st, spawn) => {
        spawn('out', css('--blue'), 'req');
        const j = await fireAdapter('unix');
        spawn('in', css('--blue'), 'res');
        return j.detail;
      },
    },
    {
      name: 'grpc', atom: 'CALL | CHANNEL', phys: 'both', where: 'adapter',
      what: 'unary = CALL · server-stream = CHANNEL, over HTTP/2', use: 'streaming device farms / gRPC backends', right: 'h2 svc',
      fire: async (st, spawn) => {
        spawn('out', css('--blue'), 'unary');
        const j = await fireAdapter('grpc');
        spawn('in', css('--blue'), 'echo');
        for (let i = 0; i < 3; i++) setTimeout(() => spawn('in', css('--purple'), `#${i}`), 350 + i * 380);
        return j.detail;
      },
    },
    {
      name: 'mqtt', atom: 'CHANNEL', phys: 'channel', where: 'adapter', mid: 'broker',
      what: 'publish = CALL into a topic · subscribe = held CHANNEL out', use: 'IoT / device-under-test event bus', right: 'subscriber',
      fire: async (st, spawn) => {
        spawn('out', css('--blue'), 'pub');
        const j = await fireAdapter('mqtt');
        setTimeout(() => spawn('out', css('--purple'), 'msg'), 420);
        st.held = true;
        return j.detail;
      },
    },
    {
      name: 'webrtc', atom: 'CHANNEL', phys: 'both', where: 'adapter',
      what: 'SDP signaling = CALL, then a peer DataChannel = CHANNEL', use: 'real-time media / low-latency apps', right: 'peer',
      fire: async (st, spawn) => {
        spawn('out', css('--blue'), 'offer');
        const j = await fireAdapter('webrtc');
        spawn('in', css('--blue'), 'answer');
        st.held = true; // the negotiated DataChannel: the wire stays lit
        return j.detail;
      },
    },
  ];

  async function fireAdapter(t: string): Promise<{ ok: boolean; ms: number; detail: string }> {
    const r = await fetch(`${BASE}/adapters/fire?t=${t}`);
    if (!r.ok) throw new Error((await r.text()).slice(0, 120));
    return r.json();
  }

  // one shared ticker draws every wire — setInterval, NOT rAF (agents must see
  // motion via captureScreenshot even when the window is occluded).
  useEffect(() => {
    for (const row of ROWS) {
      states.current[row.name] ||= {
        held: row.phys === 'channel' || row.phys === 'afferent',
        packets: [], pulses: [], midNode: row.mid, rightLabel: row.right,
      };
    }
    let lastAmbient = 0;
    let lastNow = Date.now();
    const CROSS_MS = 3400; // wall-clock time for a packet to cross A→B
    const tick = () => {
      const now = Date.now();
      // TIME-BASED advance: move by REAL elapsed ms, not a fixed per-tick delta.
      // Firefox throttles a non-foreground tab's setInterval (~300ms-1s), which
      // made the dot crawl at ~1/6 speed and hug point A — invisible, the "A/B
      // viewing problem" (2026-07-27, the hidden≠active lesson, third instance).
      // Elapsed-time motion crosses in CROSS_MS regardless of the timer rate.
      const dt = Math.min(now - lastNow, 500); // clamp a long stall to one hop
      lastNow = now;
      // ambient life on held wires: a faint event drifts in every ~5s
      if (now - lastAmbient > 5200) {
        lastAmbient = now;
        for (const n of ['websocket', 'sse']) {
          const st = states.current[n];
          if (st?.held) st.packets.push({ p: 0, dir: 'in', color: css('--yellow'), label: 'evt', speed: 0 });
        }
      }
      for (const row of ROWS) {
        const cv = canvases.current[row.name]; const st = states.current[row.name];
        if (!cv || !st) continue;
        draw(cv, row, st, now);
        for (const pk of st.packets) pk.p += dt / CROSS_MS;
        // a packet ARRIVES at the node — absorbed into a pulse, never overshoots
        for (const pk of st.packets) {
          if (pk.p >= 1) st.pulses.push({ dir: pk.dir, color: pk.color, t0: now });
        }
        st.packets = st.packets.filter((pk) => pk.p < 1);
        st.pulses = st.pulses.filter((pu) => now - pu.t0 < 600);
      }
    };
    const t = window.setInterval(tick, 40);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function draw(cv: HTMLCanvasElement, row: Row, st: WireState, now: number) {
    const w = cv.clientWidth, h = cv.clientHeight;
    if (!w) return;
    const dpr = window.devicePixelRatio || 1;
    if (cv.width !== w * dpr) { cv.width = w * dpr; cv.height = h * dpr; }
    const g = cv.getContext('2d')!;
    g.setTransform(dpr, 0, 0, dpr, 0, 0);
    g.clearRect(0, 0, w, h);
    const y = h / 2, Lx = 54, Rx = w - 64;
    // LIT derives from live traffic, not a timer: a CALL wire is lit exactly
    // while a packet is in flight (or a pulse is fading), then goes dark —
    // "request, response, gone". A held CHANNEL is always lit. (The old litUntil
    // timer un-lit after ~300ms regardless, hiding the dot — 2026-07-27.)
    const busy = st.packets.length > 0 || st.pulses.length > 0;
    const lit = st.held || busy;
    const lineColor = st.held ? css('--purple') : lit ? css('--blue') : css('--border');
    // the wire
    g.lineWidth = 2;
    g.strokeStyle = lineColor;
    g.globalAlpha = lit ? 0.85 : 0.9;
    g.setLineDash(st.held ? [] : [5, 7]);
    g.beginPath(); g.moveTo(Lx, y); g.lineTo(Rx, y); g.stroke();
    g.setLineDash([]); g.globalAlpha = 1;
    // end nodes + labels
    const node = (x: number, label: string) => {
      g.beginPath(); g.arc(x, y, 5.5, 0, 7);
      g.fillStyle = css('--bg2'); g.fill();
      g.strokeStyle = lit ? lineColor : css('--fg-faint'); g.lineWidth = 2; g.stroke();
      g.fillStyle = css('--fg-dim'); g.font = '10px ui-monospace, monospace'; g.textAlign = 'center';
      g.fillText(label, x, y + 20);
    };
    node(Lx, 'client'); node(Rx, st.rightLabel);
    if (st.midNode) node((Lx + Rx) / 2, st.midNode);
    // state caption on the wire
    g.fillStyle = css('--fg-faint'); g.font = '9.5px ui-monospace, monospace'; g.textAlign = 'left';
    g.fillText(st.held ? 'held open' : lit ? 'in flight' : 'no socket between calls', Lx + 10, y - 10);
    // arrival pulses: the destination node flashes as it ABSORBS a packet
    for (const pu of st.pulses) {
      const age = (now - pu.t0) / 600; // 0→1
      const x = pu.dir === 'out' ? Rx : Lx;
      g.globalAlpha = Math.max(0, 0.85 * (1 - age));
      g.strokeStyle = pu.color; g.lineWidth = 2;
      g.beginPath(); g.arc(x, y, 6 + age * 13, 0, 7); g.stroke();
      g.globalAlpha = 1;
    }
    // packets — big, glowing, with a fading trail behind, position CLAMPED
    // strictly between the two nodes (arrival is the pulse, never overshoot).
    const xAt = (dir: Dir, p: number) => {
      const pp = Math.max(0, Math.min(p, 1));
      return dir === 'out' ? Lx + (Rx - Lx) * pp : Rx - (Rx - Lx) * pp;
    };
    for (const pk of st.packets) {
      const x = xAt(pk.dir, pk.p);
      // trail: 5 fading ghosts behind the head (motion is legible in a still frame)
      for (let k = 5; k >= 1; k--) {
        const tx = xAt(pk.dir, pk.p - k * 0.018);
        g.globalAlpha = 0.10 * (6 - k);
        g.fillStyle = pk.color;
        g.beginPath(); g.arc(tx, y, 3 + (5 - k) * 0.7, 0, 7); g.fill();
      }
      // glow
      const grad = g.createRadialGradient(x, y, 0, x, y, 20);
      grad.addColorStop(0, pk.color); grad.addColorStop(1, 'transparent');
      g.globalAlpha = 0.55; g.fillStyle = grad;
      g.beginPath(); g.arc(x, y, 20, 0, 7); g.fill();
      // core
      g.globalAlpha = 1; g.fillStyle = pk.color;
      g.beginPath(); g.arc(x, y, 8, 0, 7); g.fill();
      g.fillStyle = css('--bg'); g.font = 'bold 8px ui-monospace, monospace'; g.textAlign = 'center'; g.textBaseline = 'middle';
      g.fillText(pk.label.slice(0, 5), x, y);
      g.textBaseline = 'alphabetic';
    }
  }

  const doFire = async (row: Row) => {
    const st = states.current[row.name];
    setRes((p) => ({ ...p, [row.name]: 'firing' }));
    const spawn = (dir: Dir, color: string, label: string) =>
      st.packets.push({ p: 0, dir, color, label, speed: 0.010 }); // ~4s wire crossing — on-wire long enough to always be caught
    try {
      const text = await row.fire(st, spawn, chan);
      setRes((p) => ({ ...p, [row.name]: { ok: true, text } }));
    } catch (e) {
      setRes((p) => ({ ...p, [row.name]: { ok: false, text: String((e as Error).message || e).slice(0, 110) } }));
    }
  };

  return (
    <section className="panel transports">
      <div className="panel-h">the wire · 8 transports = 2 physics · every fire is real</div>
      <div className="tr-lede">
        A <b className="phys-call">CALL</b> lights its wire for one round trip, then the socket is gone.
        A <b className="phys-channel">CHANNEL</b> is held — it glows, and <span className="tr-evt">events</span> arrive that were never requested.
        Fire a wire: the packets you watch are timed by the real response, and the op lands in the feed.
        {' '}<a className="tr-twowire" href="/two-wire.html" target="_blank" rel="noreferrer">the original two-wire page →</a>
      </div>
      <div className="tr-chan">{chan ? <>channel session held: <b>{chan}</b></> : <span className="bad">no channel session held — CHANNEL fires need the browser seat</span>}</div>

      {(['wire', 'adapter'] as const).map((zone) => (
        <div key={zone}>
          <div className={`tr-group${zone === 'adapter' ? ' adapter' : ''}`}>
            {zone === 'wire' ? 'the WIRE — stdlib, zero deps, raw bytes' : 'the ADAPTERS — framing/negotiation baked in (fired via adapters/loopback, witnessed by 8)'}
          </div>
          {ROWS.filter((r) => r.where === zone).map((row) => {
            const r = res[row.name];
            return (
              <div key={row.name} className="tw-row">
                <div className="tw-label">
                  <span className="tw-name">{row.name}</span>
                  <span className={`tw-atom ${row.phys === 'call' ? 'phys-call' : 'phys-channel'}`}>{row.atom}</span>
                  <span className="tw-what">{row.what}</span>
                  <span className="tw-use">{row.use}</span>
                </div>
                <canvas className="tw-canvas" ref={(el) => { canvases.current[row.name] = el; }} />
                <div className="tw-act">
                  <button className="fire" disabled={r === 'firing'} onClick={() => doFire(row)}>{r === 'firing' ? '…' : '▷ fire'}</button>
                  {r && r !== 'firing' && <div className={`tw-res ${r.ok ? 'ok' : 'bad'}`}>{r.ok ? '✓ ' : '✗ '}{r.text}</div>}
                </div>
              </div>
            );
          })}
        </div>
      ))}

      <div className="tr-foot">every fire lands in the feed · record while you fire, replay the series
        {' '}· <a className="tr-jump" onClick={() => window.dispatchEvent(new CustomEvent('8:jump', { detail: { view: 'lab' } }))}>the measured numbers → LAB</a>
        {' '}· <a className="tr-jump" onClick={() => window.dispatchEvent(new CustomEvent('8:jump', { detail: { view: 'home', q: '' } }))}>watch them land → feed</a>
      </div>
    </section>
  );
}
