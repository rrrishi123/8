import { useEffect, useState } from 'react';
import { parseCurl } from '../lib/curl';
import { fetchUrl, run, recordCtl, listSeries, replaySeries, type SeriesInfo } from '../lib/api';

// Compose / record → replay. The real source of "fire call + channel together"
// is a RECORDED SERIES: drive the UI through the wire (channel and/or request),
// each control frame is captured + seat-stamped, then REPLAY fires them together
// (both land in the middle feed). The typed box below is a manual fallback.
interface Res { line: string; ok: boolean; kind: string; summary: string; }

function plan(line: string): { kind: 'http' | 'ws'; fire: () => Promise<Res> } | null {
  const t = line.trim();
  if (!t || t.startsWith('#')) return null;
  if (t.startsWith('ws ') || t.startsWith('ws://')) {
    const rest = t.replace(/^ws:\/\/\S*\s+/, '').replace(/^ws\s+/, '');
    const m = rest.match(/^(\S+)\s+(\S+)\s*([\s\S]*)$/);
    if (!m) return null;
    const [, session, method, json] = m;
    let params: Record<string, unknown> = {};
    if (json.trim()) { try { params = JSON.parse(json); } catch { /* {} */ } }
    return { kind: 'ws', fire: async () => { try { const r = await run(session, method, params) as { type?: string }; return { line: t, kind: 'ws', ok: r?.type !== 'error', summary: JSON.stringify(r).slice(0, 120) }; } catch (e) { return { line: t, kind: 'ws', ok: false, summary: String(e) }; } } };
  }
  let parsed;
  try {
    if (t.startsWith('curl')) parsed = parseCurl(t);
    else { const mm = t.match(/^(GET|POST|PUT|DELETE|PATCH|HEAD)?\s*(https?:\/\/\S+)$/i); if (!mm) return null; parsed = { method: (mm[1] || 'GET').toUpperCase(), url: mm[2], headers: {}, body: '' }; }
  } catch { return null; }
  if (!parsed.url) return null;
  return { kind: 'http', fire: async () => { try { const r = await fetchUrl(parsed!); return { line: t, kind: 'http', ok: r.status > 0 && r.status < 400, summary: `${r.status} · ${r.latency_ms}ms` }; } catch (e) { return { line: t, kind: 'http', ok: false, summary: String(e) }; } } };
}

export function PasteCurl() {
  const [raw, setRaw] = useState('');
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<Res[]>([]);
  const [rec, setRec] = useState<{ recording: boolean; name?: string; frames?: number }>({ recording: false });
  const [name, setName] = useState('series-1');
  const [seat, setSeat] = useState('operator');
  const [series, setSeries] = useState<SeriesInfo[]>([]);

  const refresh = () => { listSeries().then(setSeries); recordCtl('').then(setRec); };
  useEffect(() => { refresh(); const t = window.setInterval(() => { if (!document.hidden) recordCtl('').then(setRec); }, 3000); return () => clearInterval(t); }, []);

  const toggleRec = async () => {
    if (rec.recording) { await recordCtl('stop'); } else { await recordCtl('start', name, seat); }
    refresh();
  };
  const doReplay = async (s: string) => {
    setBusy(true); setResults([]);
    const r = await replaySeries(s);
    setResults((r.results || []).map((x) => ({ line: `${s} #${x.method}`, kind: x.physics, ok: x.ok, summary: `${x.seat} · ${x.status}` })));
    setBusy(false);
  };

  const targets = raw.split('\n').map(plan).filter(Boolean) as { kind: 'http' | 'ws'; fire: () => Promise<Res> }[];
  const fireAll = async () => { if (!targets.length) return; setBusy(true); setResults([]); setResults(await Promise.all(targets.map((t) => t.fire()))); setBusy(false); };

  return (
    <section className="panel">
      <div className="panel-h">compose · record → replay</div>
      <div className="rec-bar">
        <button className={`rec-btn${rec.recording ? ' on' : ''}`} onClick={toggleRec} title="drive the UI through the wire while recording; replay fires the series">
          {rec.recording ? `● recording ${rec.name} (${rec.frames ?? 0})` : '○ record'}
        </button>
        {!rec.recording && <input className="rec-in" value={name} onChange={(e) => setName(e.target.value)} title="series name" />}
        {!rec.recording && <select className="rec-in" value={seat} onChange={(e) => setSeat(e.target.value)} title="seat"><option>operator</option><option>pilot</option><option>adapter</option><option>ai</option></select>}
      </div>
      <div className="series-list">
        {series.length === 0 && <div className="empty">no series yet — record while you drive a tab, then replay</div>}
        {series.map((s) => (
          <div key={s.name} className="series-row">
            <button className="series-play" onClick={() => doReplay(s.name)} title="replay — fires all frames together">▷</button>
            <span className="series-name">{s.name}</span>
            <span className="series-meta">{s.frames}f · {s.modes.join('+')} · {s.seats.join(',')}</span>
          </div>
        ))}
      </div>
      <details className="manual">
        <summary>manual compose (http + ws)</summary>
        <textarea className="curl-in" placeholder={"curl 'https://api.github.com/zen'\nws fox browsingContext.getTree {}"} value={raw} onChange={(e) => setRaw(e.target.value)} spellCheck={false} />
        <div className="row-actions"><button className="fire" disabled={!targets.length || busy} onClick={fireAll}>{busy ? '…' : `▷ Fire all (${targets.length})`}</button></div>
      </details>
      {results.length > 0 && (
        <div className="result">
          {results.map((r, i) => (<div key={i} className={`compose-res phys-${r.kind === 'ws' ? 'channel' : 'call'}`}>{r.kind === 'ws' ? 'CHAN' : 'CALL'} {r.ok ? '✓' : '✗'} {r.line.slice(0, 46)} → {r.summary}</div>))}
        </div>
      )}
    </section>
  );
}
