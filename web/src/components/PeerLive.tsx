import { useEffect, useRef, useState } from 'react';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

// PeerLive — a tab on ANOTHER host, LIVE (#9970). Pixels and input cross through
// this collector's /peer/<host>/<path> proxy to the peer collector's own /shot
// and /act, so the card behaves like a local one: you see it move, you tap, type
// and scroll it. The peer's posture (drive | view | off | unadvertised) comes back
// on X-8-Peer-Drive and is SHOWN, not assumed; the frame's age and the last act's
// witness are shown too — the card validates itself (Rishi: "the UI validates
// itself"). Firefox peers are stilled through /drawshot (leak-free), chrome
// through /shot, same as a local card.
export type PeerLiveProps = { host: string; session: string; ctx: string; url: string; stale?: boolean; live?: boolean };

export function peerShotUrl(host: string, session: string, ctx: string, w = 480): string {
  const path = session === 'fox' ? 'drawshot' : 'shot';
  return `${BASE}/peer/${encodeURIComponent(host)}/${path}?session=${encodeURIComponent(session)}&context=${encodeURIComponent(ctx)}&w=${w}`;
}
export function peerActUrl(host: string, session: string): string {
  return `${BASE}/peer/${encodeURIComponent(host)}/act?session=${encodeURIComponent(session)}`;
}

export function PeerLive({ host, session, ctx, url, stale, live = true }: PeerLiveProps) {
  const [src, setSrc] = useState('');
  const [mode, setMode] = useState('…');
  const [age, setAge] = useState<number | null>(null);
  const [err, setErr] = useState('');
  const [lastAct, setLastAct] = useState('');
  const imgRef = useRef<HTMLImageElement>(null);
  const canDrive = mode === 'drive' || mode === 'unadvertised';

  // pixels: poll a still (a persistent stream per remote tab would eat the browser's
  // per-host socket cap; a 1.5s still is enough to SEE it move, like local periphery).
  useEffect(() => {
    if (!live || stale) return;
    let dead = false, timer: number | undefined;
    const pull = async () => {
      const t0 = Date.now();
      try {
        const r = await fetch(peerShotUrl(host, session, ctx));
        setMode(r.headers.get('X-8-Peer-Drive') || 'unadvertised');
        if (!r.ok) { const j = await r.json().catch(() => ({})); throw new Error(j.why || j.error || `HTTP ${r.status}`); }
        const j = await r.json();
        if (dead) return;
        if (j.data) { setSrc(j.data); setAge(Date.now() - t0); setErr(''); }
        else setErr('peer returned no frame');
      } catch (e) { if (!dead) setErr((e as Error).message); }
      if (!dead) timer = window.setTimeout(pull, 1500);
    };
    pull();
    return () => { dead = true; if (timer) clearTimeout(timer); };
  }, [host, session, ctx, live, stale]);

  const act = async (body: Record<string, unknown>) => {
    if (!canDrive) return;
    try {
      const r = await fetch(peerActUrl(host, session), {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ context: ctx, seat: 'ai', ...body }),
      });
      const wit = r.headers.get('X-8-Witness') || '';
      setLastAct(`${body.action} → ${r.status}${wit ? ' · ' + wit.replace(/^seen · /, '') : ''}`);
      if (!r.ok) { const j = await r.json().catch(() => ({})); setErr(j.why || j.error || `act HTTP ${r.status}`); } else setErr('');
    } catch (e) { setErr((e as Error).message); }
  };
  const onTap = (e: React.MouseEvent<HTMLImageElement>) => {
    const img = e.currentTarget, r = img.getBoundingClientRect();
    act({ action: 'tap', xr: (e.clientX - r.left) / r.width, yr: (e.clientY - r.top) / r.height });
    img.focus();
  };
  const onKey = (e: React.KeyboardEvent<HTMLImageElement>) => {
    if (e.key === 'Enter') { e.preventDefault(); act({ action: 'key', text: 'Enter' }); }
    else if (e.key === 'Backspace') { e.preventDefault(); act({ action: 'key', text: 'Backspace' }); }
    else if (e.key.length === 1) { e.preventDefault(); act({ action: 'type', text: e.key }); }
  };
  useEffect(() => {
    const img = imgRef.current;
    if (!img || !canDrive) return;
    let ax = 0, ay = 0, cx = 0, cy = 0, timer: number | undefined;
    const flush = () => {
      timer = undefined;
      const r = img.getBoundingClientRect();
      act({ action: 'scroll', xr: (cx - r.left) / r.width, yr: (cy - r.top) / r.height, x2: Math.round(ax), y2: Math.round(ay) });
      ax = 0; ay = 0;
    };
    const onWheel = (e: WheelEvent) => {
      e.preventDefault(); e.stopPropagation();
      ax += e.deltaX; ay += e.deltaY; cx = e.clientX; cy = e.clientY;
      if (timer == null) timer = window.setTimeout(flush, 70);
    };
    img.addEventListener('wheel', onWheel, { passive: false });
    return () => { img.removeEventListener('wheel', onWheel); if (timer) clearTimeout(timer); };
  }, [canDrive, host, session, ctx]); // eslint-disable-line react-hooks/exhaustive-deps

  const tone = err ? '#c88' : stale ? '#c88' : '#6d8';
  const status = err ? `✗ ${err}` : stale ? 'peer stale' : src ? `live · frame ${age ?? '?'}ms via :7070 proxy` : 'connecting…';
  return (
    <div style={{ display: 'flex', flexDirection: 'column', fontFamily: 'system-ui', fontSize: 11, color: '#cdd', height: '100%' }}>
      <div style={{ padding: '4px 8px', display: 'flex', gap: 8, alignItems: 'baseline' }}>
        <span style={{ color: tone, fontWeight: 600 }}>☍ {host}</span>
        <span style={{ color: '#9aa' }}>{status}</span>
        <span style={{ marginLeft: 'auto', color: canDrive ? '#6d8' : '#ca7' }} title="the peer's advertised posture (EIGHT_PEER_DRIVE on that host): drive = pixels+input cross, view = pixels only, off = nothing">drive:{mode}</span>
      </div>
      {src
        ? <img ref={imgRef} src={src} alt={url} tabIndex={0} onClick={onTap} onKeyDown={onKey}
            style={{ width: '100%', flex: 1, objectFit: 'cover', objectPosition: 'top', cursor: canDrive ? 'crosshair' : 'not-allowed', outline: 'none', background: '#000' }} />
        : <div style={{ flex: 1, display: 'grid', placeItems: 'center', color: '#789', wordBreak: 'break-all', padding: 8 }}>{url}</div>}
      <div style={{ padding: '3px 8px', color: '#789', display: 'flex', gap: 8 }}>
        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{url}</span>
        {lastAct && <span style={{ marginLeft: 'auto', whiteSpace: 'nowrap' }} title="last act, as the peer witnessed it">{lastAct}</span>}
      </div>
    </div>
  );
}
