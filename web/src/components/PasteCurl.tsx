import { useState } from 'react';
import { parseCurl, beautify } from '../lib/curl';
import { fetchUrl } from '../lib/api';
import type { FetchResult, ParsedCurl } from '../types';

// Paste a Postman-style curl, see it beautified, Fire it (server-side via the
// collector's /fetch), see the real response.
export function PasteCurl() {
  const [raw, setRaw] = useState('');
  const [result, setResult] = useState<FetchResult | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  let parsed: ParsedCurl | null = null;
  if (raw.trim()) {
    try { parsed = parseCurl(raw); } catch { parsed = null; }
  }

  async function fire() {
    if (!parsed?.url) return;
    setBusy(true); setErr(''); setResult(null);
    try { setResult(await fetchUrl(parsed)); }
    catch (e) { setErr(String(e)); }
    finally { setBusy(false); }
  }

  return (
    <section className="panel">
      <div className="panel-h">paste a curl</div>
      <textarea
        className="curl-in"
        placeholder="curl 'https://api.github.com/zen' -H 'Accept: text/plain'"
        value={raw}
        onChange={(e) => setRaw(e.target.value)}
        spellCheck={false}
      />
      {parsed && <pre className="beautified">{beautify(parsed)}</pre>}
      <div className="row-actions">
        <button className="fire" disabled={!parsed?.url || busy} onClick={fire}>
          {busy ? '… firing' : '▷ Fire'}
        </button>
        {parsed?.url && <span className="hint">{parsed.method} {parsed.url}</span>}
      </div>
      {err && <pre className="err">{err}</pre>}
      {result && (
        <pre className="result">{`${result.status} · ${result.latency_ms}ms\n\n${pretty(result.body)}`}</pre>
      )}
    </section>
  );
}

function pretty(s: string): string {
  try { return JSON.stringify(JSON.parse(s), null, 2); } catch { return s; }
}
