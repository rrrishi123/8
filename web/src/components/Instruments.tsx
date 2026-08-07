import { useEffect, useState } from 'react';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

interface WorkItem { id: number; text: string; status: string; by: string; ts: string }
const NEXT: Record<string, string> = { todo: 'doing', doing: 'done', done: 'todo' };

// INSTRUMENTS — top-right, distinct from the world's cards because these are not
// SURFACES being witnessed, they are the witness's own gauges:
//  · experiri clock — the stopwatch AS SEEN THROUGH THE PIPELINE above the local
//    true clock; the visible gap between the two digits IS the staleness, on
//    display continuously (the telescreen that knows its own lag).
//  · work — the shared task surface: the operator writes work INTO the witness
//    here; agents read it FROM the witness before starting their own.
export function Instruments() {
  const [work, setWork] = useState<WorkItem[]>([]);
  const [add, setAdd] = useState('');
  const [now, setNow] = useState('');
  const [swTick, setSwTick] = useState(0);

  useEffect(() => { // work queue poll
    let alive = true;
    const pull = () => fetch(`${BASE}/work`).then((r) => r.json())
      .then((j) => { if (alive) setWork(j.work || []); }).catch(() => {});
    pull();
    const iv = setInterval(pull, 5000);
    return () => { alive = false; clearInterval(iv); };
  }, []);

  useEffect(() => { // true local clock at 10Hz · pipeline frame at 0.5Hz
    const p = (n: number, w: number) => String(n).padStart(w, '0');
    const c = setInterval(() => {
      const d = new Date();
      setNow(`${p(d.getHours(), 2)}:${p(d.getMinutes(), 2)}:${p(d.getSeconds(), 2)}.${p(d.getMilliseconds(), 3)}`);
    }, 100);
    const f = setInterval(() => setSwTick((t) => t + 1), 2000);
    return () => { clearInterval(c); clearInterval(f); };
  }, []);

  const refresh = () => fetch(`${BASE}/work`).then((r) => r.json()).then((j) => setWork(j.work || [])).catch(() => {});
  const submit = () => {
    const t = add.trim();
    if (!t) return;
    setAdd('');
    fetch(`${BASE}/work`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ text: t, by: 'operator' }) }).then(refresh).catch(() => {});
  };
  const cycle = (it: WorkItem) =>
    fetch(`${BASE}/work`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ id: it.id, status: NEXT[it.status] || 'todo' }) }).then(refresh).catch(() => {});

  const openCount = work.filter((w) => w.status !== 'done').length;
  return (
    <div className="instruments">
      <div className="inst-clock" title="experiri: top digits came THROUGH the capture pipeline; bottom is local truth — the gap is the witness's staleness, always on display">
        <img className="inst-sw" src={`${BASE}/drawshot?needle=stopwatch&t=${swTick}`} alt="experiri · pipeline clock" />
        <div className="inst-true">true {now}</div>
        <div className="inst-note">gap between the clocks = staleness · gaze ≈1.2s (measured)</div>
      </div>
      <div className="inst-work">
        <div className="inst-h">work · {openCount} open · agents check this first</div>
        {work.map((it) => (
          <div key={it.id} className={`work-row ${it.status}`}>
            <button className="work-dot" title={`${it.status} → ${NEXT[it.status]} (click)`} onClick={() => cycle(it)}>
              {it.status === 'todo' ? '○' : it.status === 'doing' ? '◐' : '●'}
            </button>
            <span className="work-text" title={`${it.by} · ${it.ts}`}>{it.text}</span>
          </div>
        ))}
        <input className="work-add" value={add} onChange={(e) => setAdd(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') submit(); }}
          placeholder="+ add work (Enter)" />
      </div>
    </div>
  );
}
