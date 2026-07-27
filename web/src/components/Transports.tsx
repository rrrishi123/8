import { useEffect, useState } from 'react';
import { fetchUrl, run, shot, listSessions } from '../lib/api';
import type { Session } from '../types';

// The Wire pane — the front door, made self-explanatory. Every automation
// reduces to two atoms: CALL (one request → one response) and CHANNEL (one held
// bidirectional stream). The 8 transports are those two wearing different
// clothes. Fire one and it is a REAL op through the wire — it lands in the feed,
// 8 foveates to where it happened, and it is recordable + replayable. Nothing
// here is new; it is the loop 8 already runs, named so a newcomer (human OR
// agent) sees the IP without being told.

type FireRes = { ok: boolean; summary: string; physics: 'call' | 'channel' };
interface Transport {
  name: string;
  where: 'wire' | 'adapter';
  atom: 'CALL' | 'CHANNEL' | 'CALL | CHANNEL';
  afferent?: boolean;
  what: string;
  use: string;
  // A real fire that lands in the feed, or a `proven` pointer when the browser
  // cannot originate it (it is validated in code instead — no faking).
  fire?: (chan: string | null) => Promise<FireRes>;
  proven?: string;
}

const TRANSPORTS: Transport[] = [
  {
    name: 'http', where: 'wire', atom: 'CALL',
    what: 'one request, one response, then nothing',
    use: 'every WebDriver / Appium / REST hub command',
    fire: async () => {
      const r = await fetchUrl({ method: 'GET', url: 'https://api.github.com/zen', headers: {}, body: '' });
      return { ok: r.status > 0 && r.status < 400, physics: 'call', summary: `${r.status} · ${r.latency_ms}ms · ${(r.body || '').slice(0, 40)}` };
    },
  },
  {
    name: 'websocket', where: 'wire', atom: 'CHANNEL',
    what: 'one long-lived bidirectional stream',
    use: 'live CDP / WebDriver-BiDi — every Playwright test is this',
    fire: async (chan) => {
      if (!chan) return { ok: false, physics: 'channel', summary: 'no channel session held' };
      const r = await run(chan, 'browsingContext.getTree', {}) as { result?: { contexts?: unknown[] }; type?: string };
      const n = r?.result?.contexts?.length;
      return { ok: r?.type !== 'error', physics: 'channel', summary: n != null ? `getTree → ${n} tabs on the held socket` : JSON.stringify(r).slice(0, 48) };
    },
  },
  {
    name: 'sse', where: 'wire', atom: 'CHANNEL', afferent: true,
    what: 'server pushes, client only listens (text/event-stream)',
    use: 'the feed you are watching right now IS this',
    proven: 'the middle feed is a live SSE channel — every row arrived by server push',
  },
  {
    name: 'mjpeg', where: 'wire', atom: 'CHANNEL', afferent: true,
    what: 'multipart/x-mixed-replace — the afferent media plane',
    use: 'live device / browser screencast',
    fire: async (chan) => {
      if (!chan) return { ok: false, physics: 'channel', summary: 'no channel session held' };
      const data = await shot(chan);
      return { ok: !!data, physics: 'channel', summary: data ? `captured a frame · ${Math.round(data.length / 1024)}kb (the media plane)` : 'no frame' };
    },
  },
  {
    name: 'unix_socket', where: 'wire', atom: 'CALL | CHANNEL',
    what: 'the same two atoms over a unix dialer — no TCP port',
    use: 'portless local daemons (dockerd, a driver sidecar)',
    proven: 'http+unix:// & ws+unix:// — probed both halves in http-mcp/cmd/mcp/transports_conformance_test.go',
  },
  {
    name: 'grpc', where: 'adapter', atom: 'CALL | CHANNEL',
    what: 'unary = CALL, server-stream = CHANNEL, over HTTP/2',
    use: 'streaming device farms / gRPC backends',
    proven: 'real HTTP/2 loopback, no protoc — adapters/grpc/grpc_test.go',
  },
  {
    name: 'mqtt', where: 'adapter', atom: 'CHANNEL',
    what: 'publish = CALL, subscribe = CHANNEL (held broker)',
    use: 'IoT / device-under-test event bus',
    proven: 'hand-rolled MQTT 3.1.1 broker + client — adapters/mqtt/mqtt_test.go',
  },
  {
    name: 'webrtc', where: 'adapter', atom: 'CHANNEL',
    what: 'SDP signaling = CALL, then a peer DataChannel = CHANNEL',
    use: 'real-time media / low-latency datachannel apps',
    proven: 'signaling offer→answer CALL (the only wire-visible surface) — adapters/webrtc/webrtc_test.go',
  },
];

export function Transports() {
  const [chan, setChan] = useState<string | null>(null);
  const [res, setRes] = useState<Record<string, FireRes | '…'>>({});

  useEffect(() => {
    const load = () => listSessions()
      .then((ss: Session[]) => setChan(ss.find((s) => s.physics === 'channel' && s.status !== 'disconnected')?.id || null))
      .catch(() => {});
    load();
    const t = window.setInterval(load, 3000);
    return () => clearInterval(t);
  }, []);

  const doFire = async (t: Transport) => {
    if (!t.fire) return;
    setRes((p) => ({ ...p, [t.name]: '…' }));
    try { const r = await t.fire(chan); setRes((p) => ({ ...p, [t.name]: r })); }
    catch (e) { setRes((p) => ({ ...p, [t.name]: { ok: false, physics: 'call', summary: String(e).slice(0, 60) } })); }
  };

  const group = (where: 'wire' | 'adapter') => TRANSPORTS.filter((t) => t.where === where);

  const row = (t: Transport) => {
    const r = res[t.name];
    const phys = t.atom.startsWith('CHANNEL') ? 'channel' : 'call';
    return (
      <div key={t.name} className={`tr-row phys-${phys}`}>
        <span className="tr-name">{t.name}</span>
        <span className={`tr-atom phys-${phys}`}>{t.atom}{t.afferent ? ' · afferent' : ''}</span>
        <span className="tr-what">{t.what}<span className="tr-use">{t.use}</span></span>
        <span className="tr-act">
          {t.fire
            ? <button className="fire" disabled={r === '…'} onClick={() => doFire(t)}>{r === '…' ? '…' : '▷ fire'}</button>
            : <span className="tr-proven-tag">proven ↓</span>}
        </span>
        <span className="tr-res">
          {t.fire && r && r !== '…' && <span className={r.ok ? 'ok' : 'bad'}>{r.ok ? '✓ ' : '✗ '}{r.summary}</span>}
          {!t.fire && t.proven && <span className="tr-proven">{t.proven}</span>}
        </span>
      </div>
    );
  };

  return (
    <section className="panel transports">
      <div className="panel-h">the wire · 8 transports = 2 atoms</div>
      <div className="tr-lede">
        Every automation reduces to two atoms: <b className="phys-call">CALL</b> (one request → one response) and <b className="phys-channel">CHANNEL</b> (one held bidirectional stream).
        The 8 below are those two in different clothes. <b>Fire one</b> — it is a real op through the wire: it lands in the feed, 8 foveates to where it happened, and it is recordable → replayable. This is not a demo beside 8; it is the loop 8 already runs.
        {' '}<a className="tr-twowire" href="/two-wire.html" target="_blank" rel="noreferrer">watch the two physics, animated →</a>
      </div>
      <div className="tr-chan">{chan ? <>channel session held: <b>{chan}</b> — CHANNEL fires drive it live</> : <span className="bad">no channel session held — start Firefox/8 to fire CHANNEL ops</span>}</div>

      <div className="tr-group">the WIRE — stdlib, zero deps, raw bytes</div>
      {group('wire').map(row)}
      <div className="tr-group adapter">the ADAPTER — adds framing / routing / negotiation, the wire never sees it</div>
      {group('adapter').map(row)}

      <div className="tr-foot">fired ops appear in the middle feed with <span className="phys-call">call</span>/<span className="phys-channel">channel</span> physics · click one to inspect · record while you fire, then replay the series</div>
    </section>
  );
}
