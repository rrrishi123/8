import { useEffect, useState } from 'react';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

// BUDGET HUD — always visible in the statusline. The flip switch made legible:
// the 5h window with its research ceiling (50%) and hard cap (95%) marked, the
// current PHASE (research=work hard, conserve=hold heavy work), 7d, and how
// fresh the reading is. Shown to both end-users of 8: the operator and the minds.
type Win = { utilization: number; status: string; reset_in_s: number };
type Budget = { phase: string; research_ok: boolean; research_ceil: number; cap: number; five_h_util: number;
  windows: Record<string, Win>; gated: boolean };
type Resp = { budget: Budget | null; observed_age_s: number; sensors_armed: number };

const hhm = (s?: number) => s == null ? '' : s > 3600 ? `${Math.floor(s / 3600)}h${Math.round((s % 3600) / 60)}m` : `${Math.round(s / 60)}m`;

export function BudgetHud() {
  const [r, setR] = useState<Resp | null>(null);
  const [poking, setPoking] = useState(false);
  const poke = async () => { if (poking) return; setPoking(true); try { const j = await (await fetch(`${BASE}/budget/poke`, { method: 'POST' })).json(); setR((p) => ({ ...(p as Resp), ...j })); } catch { /* */ } setPoking(false); };
  useEffect(() => {
    let dead = false;
    const tick = () => fetch(`${BASE}/budget`).then((x) => x.json()).then((j) => { if (!dead) setR(j); }).catch(() => {});
    tick(); const t = setInterval(tick, 4000);
    return () => { dead = true; clearInterval(t); };
  }, []);
  const b = r?.budget;
  if (!b) return <span className="hud hud-none" title="no budget sensor yet — a tapped pane must make an API call">◇ budget —</span>;
  const five = b.five_h_util, sevenD = b.windows['7d']?.utilization ?? 0;
  const phase = b.phase;
  const stale = (r?.observed_age_s ?? 0) > 900;
  return (
    <span className={`hud hud-${phase}`} title={`5h ${(five*100).toFixed(0)}% · research ceiling ${(b.research_ceil*100).toFixed(0)}% · hard cap ${(b.cap*100).toFixed(0)}% · 7d ${(sevenD*100).toFixed(0)}% · observed ${r?.observed_age_s}s ago · ${r?.sensors_armed} sensors`}>
      <span className={`hud-dot hud-dot-${phase}`} />
      <b>{phase === 'research' ? 'RESEARCH' : 'CONSERVE'}</b>
      <span className="hud-bar" aria-hidden>
        <span className="hud-fill" style={{ width: `${Math.min(100, five * 100)}%` }} />
        <span className="hud-mark hud-ceil" style={{ left: `${b.research_ceil * 100}%` }} />
        <span className="hud-mark hud-cap" style={{ left: `${b.cap * 100}%` }} />
      </span>
      <span className="hud-num">5h {(five*100).toFixed(0)}%</span>
      <span className="hud-num dim">7d {(sevenD*100).toFixed(0)}%</span>
      <button className="hud-refresh" title="renew the reading (fires a trivial call so fresh rate-limit headers arrive)" onClick={(e) => { e.stopPropagation(); poke(); }}>{poking ? "…" : "⟳"}</button>
      {b.gated && <span className="hud-gated">⏸ resets {hhm(Object.values(b.windows).find((w) => w.status === 'rejected')?.reset_in_s)}</span>}
      {stale && <span className="hud-stale" title="no recent API traffic — last-known">·stale {hhm(r?.observed_age_s)}</span>}
    </span>
  );
}
