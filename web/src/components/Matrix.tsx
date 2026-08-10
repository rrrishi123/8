import { useEffect, useState } from 'react';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

interface Cell { s: string; detail: string }
interface Row { kind: string; cells: Record<string, Cell> }
interface MatrixData { cols: string[]; rows: Row[]; unfound: number; legend: string }

// MATRIX — surfaces × senses coverage: THE MAP OF THE UNFOUND (work #6). Every
// cell is a probed/declared verdict; the empty (unfound) cells are the point —
// a sense a surface COULD expose and doesn't, where something stands seen-able
// but unseen. Not N/A (grey, inapplicable) — unfound (amber, a candidate).
const CH: Record<string, string> = { live: '●', yes: '✓', na: '·', unfound: '◍' };
export function Matrix() {
  const [m, setM] = useState<MatrixData | null>(null);
  const [hover, setHover] = useState('');
  useEffect(() => {
    let alive = true;
    const pull = () => fetch(`${BASE}/matrix`).then((r) => r.json()).then((j) => { if (alive) setM(j); }).catch(() => {});
    pull();
    const iv = setInterval(pull, 15000);
    return () => { alive = false; clearInterval(iv); };
  }, []);
  if (!m) return null;
  const short = (c: string) => c.replace('frame·', '').replace('·eval', '').replace('·push', '').replace('·ledger', '');
  return (
    <div className="inst-matrix">
      <div className="inst-h">surfaces × senses · {m.unfound} unfound — the map</div>
      <table className="mx-tbl">
        <thead><tr><th></th>{m.cols.map((c) => <th key={c} title={c}>{short(c)}</th>)}</tr></thead>
        <tbody>
          {m.rows.map((r) => (
            <tr key={r.kind}>
              <td className="mx-kind">{r.kind}</td>
              {m.cols.map((c) => {
                const cell = r.cells[c] || { s: 'na', detail: '' };
                return <td key={c} className={`mx-cell mx-${cell.s}`}
                  title={`${r.kind} · ${c}\n${cell.s.toUpperCase()}: ${cell.detail}`}
                  onMouseEnter={() => setHover(`${cell.s === 'unfound' ? '◍ UNFOUND' : cell.s} · ${cell.detail}`)}>
                  {CH[cell.s] || '?'}</td>;
              })}
            </tr>
          ))}
        </tbody>
      </table>
      <div className="mx-legend">{hover || '● probed · ✓ built · · n/a · ◍ unfound (could exist, unbuilt)'}</div>
    </div>
  );
}
