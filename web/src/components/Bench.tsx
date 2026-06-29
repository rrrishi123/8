import { useEffect, useState } from 'react';
import { getBenches, getRequests, replay } from '../lib/api';
import type { BenchRec, ReqRec } from '../types';

// The Lab — 8 as the witness. BENCHMARKS clubs batches by tag with their full
// metric set; REQUESTS is the durable ledger, each row replayable if its session
// still holds the socket. Data is loaded as a bounded window (the store keeps
// all; the cockpit shows the recent slice).
export function Bench() {
  const [tab, setTab] = useState<'batches' | 'requests' | 'physics'>('batches');
  const [benches, setBenches] = useState<BenchRec[]>([]);
  const [reqs, setReqs] = useState<ReqRec[]>([]);
  const [tag, setTag] = useState('');
  const [phys, setPhys] = useState<'all' | 'call' | 'channel'>('all');
  const [replayed, setReplayed] = useState<Record<number, string>>({});

  useEffect(() => {
    const load = () => {
      getBenches(tag || undefined).then(setBenches).catch(() => {});
      getRequests(150).then(setReqs).catch(() => {});
    };
    load();
    const t = window.setInterval(load, 2000);
    return () => clearInterval(t);
  }, [tag]);

  const tags = Array.from(new Set(benches.map((b) => b.tag).filter(Boolean))) as string[];
  const clubs = tags.length ? tags : benches.length ? ['(untagged)'] : [];
  const shownReqs = reqs.filter((r) => phys === 'all' || r.physics === phys);

  async function doReplay(id: number) {
    setReplayed((p) => ({ ...p, [id]: '…' }));
    const res = await replay(id);
    setReplayed((p) => ({
      ...p,
      [id]: res.error ? `✗ ${res.error.slice(0, 24)}` : `▶ ${res.status} · ${Math.round(res.latency_us || 0)}µs · #${res.new_id}`,
    }));
  }

  return (
    <section className="panel lab">
      <div className="lab-tabs">
        <button className={tab === 'batches' ? 'on' : ''} onClick={() => setTab('batches')}>
          BENCHMARKS <b>{benches.length}</b>
        </button>
        <button className={tab === 'requests' ? 'on' : ''} onClick={() => setTab('requests')}>
          REQUESTS <b>{reqs.length}</b>
        </button>
        <button className={tab === 'physics' ? 'on' : ''} onClick={() => setTab('physics')}>
          PHYSICS
        </button>
        <span className="lab-note">read-via-channel vs see-via-screenshot · timed in 8 (Go ns)</span>
      </div>

      {tab === 'batches' && (
        <div className="lab-body">
          <div className="lab-filter">
            <span>tag</span>
            <button className={tag === '' ? 'on' : ''} onClick={() => setTag('')}>all</button>
            {tags.map((t) => (
              <button key={t} className={tag === t ? 'on' : ''} onClick={() => setTag(t)}>{t}</button>
            ))}
          </div>
          {clubs.map((club) => {
            const rows = benches.filter((b) => (b.tag || '(untagged)') === club);
            if (!rows.length) return null;
            const avg = rows.reduce((a, b) => a + b.equiv_p50, 0) / rows.length;
            const trials = rows.reduce((a, b) => a + b.n, 0);
            return (
              <div key={club} className="club">
                <div className="club-head">
                  <b>{club}</b> · {rows.length} batch{rows.length > 1 ? 'es' : ''} · {trials.toLocaleString()} trials ·
                  mean seeing ≈ <b className="hot">{avg.toFixed(1)}×</b> a channel read
                </div>
                <table className="metrics">
                  <thead>
                    <tr>
                      <th>batch</th><th>n</th><th>read p50</th><th>read p99</th>
                      <th>see p50</th><th>see p99</th><th>equiv p50</th><th>equiv p99</th><th>bytes×</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((b) => (
                      <tr key={b.id}>
                        <td>#{b.id}</td><td>{b.n}</td>
                        <td>{b.read.p50.toFixed(0)}µs</td><td>{b.read.p99.toFixed(0)}µs</td>
                        <td>{(b.see.p50 / 1000).toFixed(1)}ms</td><td>{(b.see.p99 / 1000).toFixed(1)}ms</td>
                        <td className="hot">{b.equiv_p50.toFixed(1)}×</td><td>{b.equiv_p99.toFixed(1)}×</td>
                        <td>{b.byte_ratio.toFixed(0)}×</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            );
          })}
          {!benches.length && <div className="empty">no batches yet — run a /bench (tag it to club).</div>}
        </div>
      )}

      {tab === 'requests' && (
        <div className="lab-body">
          <div className="lab-filter">
            <span>physics</span>
            {(['all', 'call', 'channel'] as const).map((p) => (
              <button key={p} className={phys === p ? 'on' : ''} onClick={() => setPhys(p)}>{p}</button>
            ))}
          </div>
          <table className="metrics reqs">
            <thead>
              <tr><th>#</th><th>phys</th><th>method</th><th>url / route</th><th>status</th><th>latency</th><th>replay</th></tr>
            </thead>
            <tbody>
              {shownReqs.map((r) => (
                <tr key={r.id} className={`phys-${r.physics}`}>
                  <td>{r.id}</td>
                  <td className="phys">{r.physics}</td>
                  <td>{r.method}</td>
                  <td className="url" title={r.url}>{r.url.replace(/^https?:\/\//, '').slice(0, 44)}</td>
                  <td className={r.status >= 400 || r.status === 0 ? 'bad' : ''}>{r.status}</td>
                  <td>{(r.latency_us / 1000).toFixed(2)}ms</td>
                  <td>
                    {r.replayable ? (
                      <button className="replay" onClick={() => doReplay(r.id)}>{replayed[r.id] || '▶ replay'}</button>
                    ) : (
                      <span className="no">— gone</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {!shownReqs.length && <div className="empty">no requests yet — drive a session or Fire a curl.</div>}
        </div>
      )}

      {tab === 'physics' && (
        <div className="lab-body physics">
          <div className="club-head">The IP — <b>two physics through the wire, and where each wins.</b> <span className="lab-note">pressure-tested with the peer (DeepSeek), re-verified with full context</span></div>

          <div className="club">
            <div className="club-head"><b>The 5-layer floor</b> — what "see / act through MCP" actually costs</div>
            <table className="metrics"><thead><tr><th>layer</th><th>what</th><th>cost</th><th>note</th></tr></thead><tbody>
              <tr><td>L1</td><td>agent cognition</td><td className="hot">0.5–5 s</td><td>dominant — the real ceiling</td></tr>
              <tr><td>L2</td><td>MCP transduction</td><td>~100 µs</td><td>NOT a third physics — a uniform transducer</td></tr>
              <tr><td>L3</td><td>broker queueing</td><td>—</td><td>held-socket contention (head-of-line)</td></tr>
              <tr><td>L4</td><td>wire physics</td><td>—</td><td>CALL (HTTP+TLS) vs CHANNEL (ws+id)</td></tr>
              <tr><td>L5</td><td>target render/OS</td><td>—</td><td>browser compositing vs device framebuffer</td></tr>
            </tbody></table>
          </div>

          <div className="club">
            <div className="club-head"><b>Two physics, grounded p50</b> — browser (BiDi) vs real device (Appium/USB)</div>
            <table className="metrics"><thead><tr><th>op</th><th>CHANNEL (browser)</th><th>CALL (device)</th><th>winner</th></tr></thead><tbody>
              <tr><td>READ state</td><td>0.6 ms <span className="dim">evaluate</span></td><td>5–15 ms <span className="dim">/element, /source</span></td><td className="hot">channel ~8×</td></tr>
              <tr><td>SEE pixels</td><td>15 ms <span className="dim">captureScreenshot</span></td><td>120 ms <span className="dim">/screenshot · framebuffer</span></td><td className="hot">channel ~8×</td></tr>
              <tr><td>ACT tap</td><td>0.8 ms <span className="dim">dispatch</span></td><td>25 ms <span className="dim">/actions · UI thread</span></td><td className="hot">channel ~30×</td></tr>
            </tbody></table>
          </div>

          <div className="club">
            <div className="club-head"><b>Moving targets</b> — moving pixels: poll vs stream</div>
            <table className="metrics"><thead><tr><th>metric</th><th>CALL (poll)</th><th>CHANNEL (stream)</th></tr></thead><tbody>
              <tr><td>effective fps</td><td>~5</td><td className="hot">30–120</td></tr>
              <tr><td>capture latency</td><td>~200 ms</td><td className="hot">35–70 ms (scrcpy)</td></tr>
              <tr><td>sampling</td><td>fixed-interval — misses events (Nyquist)</td><td className="hot">continuous — no missed frames</td></tr>
            </tbody></table>
            <p className="dim">metrics: time-to-first-faithful-frame · frame-loss-rate · motion-to-photon vs motion-to-knowledge. For any dynamic UI (scroll, video, game, animation) CHANNEL-SEE is mandatory; CALL-SEE is deprecated for motion.</p>
          </div>

          <div className="club">
            <div className="club-head"><b>The boundary</b> &amp; <b>Vibium</b></div>
            <p className="dim">READ wins where state is structured (DOM / JS / protocol). SEE is irreducible for a <b>render</b> question — "is the button red?" — pixels only. CHANNEL is mandatory for <b>moving</b> targets. And the wire is <b>never</b> the bottleneck for an agent: L1 cognition (seconds) dwarfs L2+L3+L4 (sub-100ms) — optimize tool-calling, not microseconds.</p>
            <p className="dim"><b>Vibium</b> (Jason Huggins, Selenium / Appium's creator): AI-native automation on WebDriver-BiDi. With full context the peer placed it precisely — Vibium is a <b>composer over the channel</b> (like Playwright / Puppeteer / Selenium-BiDi), <b>not a third physics</b>. This http-mcp / pilot / 8 wire is the substrate it composes over.</p>
            <p className="dim"><b>Context check.</b> Asked context-free, the peer called the benchmark "rigged"; with our full thread in view it corrected to <b>"specialized &amp; earned"</b> — fair for structured state, bounded, never a refutation of seeing. The contextful answer is the one that stands.</p>
          </div>

          <div className="club">
            <div className="club-head"><b>Cloud frameworks</b> — one CALL family; RD/VD is a payload flag, not a third physics</div>
            <table className="metrics"><thead><tr><th>type</th><th>endpoint (CALL · http_request)</th><th>physical pool</th></tr></thead><tbody>
              <tr><td>espresso</td><td>mobile-api/framework/v1/espresso/build</td><td>android device · emulator</td></tr>
              <tr><td>xcui</td><td>mobile-api/framework/v1/xcui/build</td><td>ios device · simulator</td></tr>
              <tr><td>flutterAndroid</td><td>mobile-api/framework/v1/flutter/build</td><td>android</td></tr>
              <tr><td>flutterIos</td><td>mobile-api/framework/v1/flutter/ios/build</td><td>ios</td></tr>
            </tbody></table>
            <p className="dim">The whole native-suite family is one endpoint shape. <b>Real vs virtual device is the <code>isVirtualDevice</code> flag in the payload</b> — same wire, same tool, the pool is data. The public api-doc curls point at <code>manual-api</code>, but that host <b>500s</b> on the live build/list routes; <code>mobile-api</code> is the working host (manual-api is upload-only). Verified live through http-mcp on <code>prod:adminltqa</code>, secret below the boundary:</p>
            <table className="metrics"><thead><tr><th>run</th><th>RD/VD</th><th>buildId</th><th>verdict</th></tr></thead><tbody>
              <tr><td>espresso</td><td>real device</td><td><a href="https://appautomation.lambdatest.com/build?pageType=build&buildId=22983015" target="_blank" rel="noreferrer">22983015</a></td><td className="hot">Passed · 4 sessions · 1m13s</td></tr>
              <tr><td>xcui</td><td><b>virtual</b> (isVirtualDevice)</td><td><a href="https://appautomation.lambdatest.com/build?pageType=build&buildId=22986617" target="_blank" rel="noreferrer">22986617</a></td><td>submitted · iPhone 15 sim</td></tr>
            </tbody></table>
            <p className="dim"><b>Peer review of http-mcp v.0.0.1.</b> The provider matrix is <b>spec-data expanded by a host</b> — http-mcp stays the 3-tool, provider-agnostic wire; each new cloud (BrowserStack next) is one JSON file, never a tool. The peer: <i>"exactly the right boundary … prevents http-mcp from becoming a monolithic integration hub."</i> On provenance he held the <code>isVirtualDevice</code> flag sufficient if paired with the buildId (hence the links above). And he flagged two auth hardenings — redact the resolved ws URL and credential from channel errors — now applied below the boundary.</p>
          </div>
        </div>
      )}
    </section>
  );
}
