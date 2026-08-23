import { useEffect, useRef, useState } from 'react';
import { innerHost, type InnerHost } from '../lib/api';
import { Matrix } from './Matrix';
import { useLocal } from './Dock';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

interface WorkItem { id: number; text: string; status: string; by: string; ts: string; prio?: number; deps?: number[]; assignee?: string }
const NEXT: Record<string, string> = { todo: 'doing', doing: 'done', done: 'todo' };

// INSTRUMENTS — the witness's own gauges. They are NOT the world (that's the
// canvas); they're meta. So they live COLLAPSED as a compact chip row top-right,
// each expanding on click — the real view stays the surfaces, not my dials.
// (2026-08-10: the stacked-always-open panels ate 79% of viewport height —
// clutter. A chip row + on-demand expand reclaims the column.)
export function Instruments() {
  const [work, setWork] = useState<WorkItem[]>([]);
  const [add, setAdd] = useState('');
  const [now, setNow] = useState('');
  const [swTick, setSwTick] = useState(0);
  const [open, setOpen] = useLocal<string>('instrOpen', ''); // '', 'clock', 'work', 'matrix'

  useEffect(() => {
    let alive = true;
    const pull = () => fetch(`${BASE}/work`).then((r) => r.json())
      .then((j) => { if (alive) setWork(j.work || []); }).catch(() => {});
    pull();
    const iv = setInterval(pull, 5000);
    return () => { alive = false; clearInterval(iv); };
  }, []);
  useEffect(() => {
    const p = (n: number, w: number) => String(n).padStart(w, '0');
    const c = setInterval(() => { const d = new Date(); setNow(`${p(d.getHours(), 2)}:${p(d.getMinutes(), 2)}:${p(d.getSeconds(), 2)}`); }, 1000);
    const f = setInterval(() => setSwTick((t) => t + 1), 2000);
    return () => { clearInterval(c); clearInterval(f); };
  }, []);

  const refresh = () => fetch(`${BASE}/work`).then((r) => r.json()).then((j) => setWork(j.work || [])).catch(() => {});
  const submit = () => { const t = add.trim(); if (!t) return; setAdd(''); fetch(`${BASE}/work`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ text: t, by: 'operator' }) }).then(refresh).catch(() => {}); };
  const cycle = (it: WorkItem) => fetch(`${BASE}/work`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ id: it.id, status: NEXT[it.status] || 'todo' }) }).then(refresh).catch(() => {});
  const reprio = (it: WorkItem, d: number) =>
    fetch(`${BASE}/work`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ id: it.id, prio: (it.prio || 0) + d }) }).then(refresh).catch(() => {});
  const dragId = useRef<number | null>(null);
  // DRAG-DROP reorder: drop the dragged todo before the target, then reassign
  // descending prios across all todos so the visual order sticks in the picker.
  const dropOn = (target: WorkItem) => {
    const src = dragId.current; dragId.current = null;
    if (!src || src === target.id || target.status !== 'todo') return;
    const ids = ordered.filter((w) => w.status === 'todo').map((t) => t.id).filter((id) => id !== src);
    const ti = ids.indexOf(target.id);
    ids.splice(ti < 0 ? ids.length : ti, 0, src);
    Promise.all(ids.map((id, i) => fetch(`${BASE}/work`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ id, prio: ids.length - i }) }))).then(refresh).catch(() => {});
  };
  const [playlist, setPlaylist] = useState(false);
  useEffect(() => { fetch(`${BASE}/work/playlist`).then((r) => r.json()).then((j) => setPlaylist(!!j.playlist)).catch(() => {}); }, []);
  const togglePlaylist = () => fetch(`${BASE}/work/playlist?on=${playlist ? 0 : 1}`).then((r) => r.json()).then((j) => { setPlaylist(!!j.playlist); refresh(); }).catch(() => {});
  // ORDER the queue the way the PICKER sees it: todos by prio desc then id;
  // done/doing keep their place after. The operator's ↑↓ move the queue visibly.
  const ordered = [...work].sort((a, b) => {
    const rank = (w: WorkItem) => (w.status === 'todo' ? 0 : w.status === 'doing' ? 1 : 2);
    if (rank(a) !== rank(b)) return rank(a) - rank(b);
    if (a.status === 'todo') return (b.prio || 0) - (a.prio || 0) || a.id - b.id;
    return a.id - b.id;
  });
  const openCount = work.filter((w) => w.status !== 'done').length;
  const [inner, setInner] = useState<InnerHost | null>(null);
  useEffect(() => {
    if (open !== 'inner') return;
    let dead = false;
    const pull = () => innerHost().then((d) => { if (!dead) setInner(d); }).catch(() => {});
    pull(); const t = setInterval(pull, 5000);
    return () => { dead = true; clearInterval(t); };
  }, [open]);
  const tog = (k: string) => setOpen((cur) => (cur === k ? '' : k));

  return (
    <div className="instruments">
      <div className="inst-chips">
        <button className={`inst-chip${open === 'clock' ? ' on' : ''}`} onClick={() => tog('clock')} title="experiri — the witness's staleness, on display">⏱ {now}</button>
        <button className={`inst-chip${open === 'work' ? ' on' : ''}`} onClick={() => tog('work')} title="shared work surface">✓ {openCount}</button>
        <button className={`inst-chip${open === 'matrix' ? ' on' : ''}`} onClick={() => tog('matrix')} title="surfaces × senses — the map of the unfound">▦</button>
        <button className={`inst-chip${open === 'inner' ? ' on' : ''}`} onClick={() => tog('inner')} title="inner host — this machine's containers + colima VM (the 4-system observing its own containerized incarnation)">▣ host</button>
      </div>
      {open === 'clock' && (
        <div className="inst-clock" title="top digits came THROUGH the capture pipeline; the gap to true time is the witness's staleness">
          <img className="inst-sw" src={`${BASE}/drawshot?needle=stopwatch&t=${swTick}`} alt="experiri · pipeline clock" />
          <div className="inst-note">pipeline clock vs true — the gap is staleness · gaze ≈1.2s</div>
        </div>
      )}
      {open === 'work' && (
        <div className="inst-work">
          <div className="inst-h">
            work · {openCount} open · drag to reorder
            <button className={`work-play${playlist ? ' on' : ''}`} title={playlist ? 'playlist ON — the queue runs itself, one by one' : '▶ run all: pick→do→done→pick, hands-free'}
              onClick={togglePlaylist}>{playlist ? '⏸ playing' : '▶ run all'}</button>
          </div>
          {ordered.map((it) => (
            <div key={it.id} className={`work-row ${it.status}`}
              draggable={it.status === 'todo'}
              onDragStart={() => { dragId.current = it.id; }}
              onDragOver={(e) => { if (it.status === 'todo') e.preventDefault(); }}
              onDrop={() => dropOn(it)}>
              <button className="work-dot" title={`${it.status} → ${NEXT[it.status]}`} onClick={() => cycle(it)}>{it.status === 'todo' ? '•' : it.status === 'doing' ? '◐' : '✓'}</button>
              <span className="work-edge" title="who incepted → which pane it's routed to (#849)">
                <span className="we-from">{it.by || '·'}</span>
                <span className="we-arrow">→</span>
                <span className={`we-to${it.assignee ? '' : ' none'}`}>{it.assignee || 'pool'}</span>
              </span>
              <span className="work-text" title={`#${it.id} · ${it.by}${it.assignee ? ' → ' + it.assignee : ''} · ${it.ts}${it.prio ? ' · prio ' + it.prio : ''}`}>{it.text}</span>
              {it.status === 'todo' && (
                <span className="work-prio">
                  <button title="raise priority" onClick={() => reprio(it, 1)}>▲</button>
                  <button title="lower priority" onClick={() => reprio(it, -1)}>▼</button>
                </span>
              )}
            </div>
          ))}
          <input className="work-add" value={add} onChange={(e) => setAdd(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') submit(); }} placeholder="+ add work (Enter)" />
        </div>
      )}
      {open === 'inner' && (
        <div className="inst-inner">
          <div className="inst-h">inner host{inner ? ` · ${inner.at ? new Date(inner.at).toLocaleTimeString() : '…'}` : ' · …'}</div>
          {inner?.vms && inner.vms.length > 0 && (
            <div className="ih-vms">
              {inner.vms.map((v) => (
                <div key={v.name} className="ih-vm" title="colima VM">
                  <span className="ih-name">{v.name}</span>
                  <span className={`ih-status ${v.status === 'Running' ? 'up' : 'down'}`}>{v.status}</span>
                  <span className="ih-spec">{v.cpus}cpu · {v.memory} · {v.disk} · {v.runtime}</span>
                </div>
              ))}
            </div>
          )}
          <div className="ih-ctable">
            <div className="ih-crow ih-chead"><span>container</span><span>cpu</span><span>mem</span><span>pids</span></div>
            {inner?.containers?.map((c) => (
              <div key={c.name} className="ih-crow" title={`${c.name} · net ${c.net_io} · blk ${c.blk_io} · mem ${c.mem_pc}`}>
                <span className="ih-cname">{c.name}</span>
                <span>{c.cpu}</span>
                <span>{c.mem.split(' / ')[0]}</span>
                <span>{c.pids}</span>
              </div>
            ))}
            {inner && !inner.docker_ok && <div className="ih-none">docker not reachable</div>}
            {inner && inner.docker_ok && (!inner.containers || inner.containers.length === 0) && <div className="ih-none">no containers running</div>}
          </div>
        </div>
      )}
      {open === 'matrix' && <Matrix />}
    </div>
  );
}
