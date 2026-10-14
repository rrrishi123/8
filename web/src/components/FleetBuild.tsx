import { useEffect, useState } from 'react';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';
type Peer = { host: string; stale?: boolean; extra?: { build?: string } };

// Build provenance is the running binary's /health response, never the checkout.
export function FleetBuild() {
  const [state, setState] = useState<{ build: string; peers: Peer[]; unavailable: boolean }>({ build: '', peers: [], unavailable: true });
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      try {
        const get = async (route: string) => {
          const response = await fetch(`${BASE}/${route}`, { signal: controller.signal });
          if (!response.ok) throw new Error(`HTTP ${response.status}`);
          return response.json();
        };
        const [health, peers] = await Promise.all([get('health'), get('peers')]);
        if (!controller.signal.aborted) setState({ build: health.build || '', peers: peers.peers || [], unavailable: !health.alive });
      } catch { if (!controller.signal.aborted) setState(s => ({ ...s, unavailable: true })); }
      if (!controller.signal.aborted) timer = setTimeout(tick, 5000);
    };
    tick();
    return () => { controller.abort(); clearTimeout(timer); };
  }, []);
  const known = /^[a-f0-9]{40}$/.test(state.build);
  return <span aria-label="Running builds" style={{ display: 'flex', gap: 6, fontSize: 10, maxWidth: 270, overflowX: 'auto' }}>
    <span data-build-host="local" data-build-sha={state.build}
      style={{ color: known && !state.unavailable ? '#42b883' : '#dfab50', whiteSpace: 'nowrap' }}
      title={`Local running collector: ${state.build || 'unknown'}${state.unavailable ? ' (unavailable)' : ''}`}>
      build {known ? state.build.slice(0, 7) : 'unknown'}{state.unavailable ? ' stale' : ''}
    </span>
    {state.peers.map(peer => {
      const build = peer.extra?.build || '';
      const valid = /^[a-f0-9]{40}$/.test(build);
      const status = state.unavailable || peer.stale ? 'stale' : !known || !valid ? 'unknown' : build === state.build ? 'match' : 'drift';
      return <span key={peer.host} data-build-host={peer.host} data-build-sha={build} data-build-status={status}
        title={`${peer.host}: ${build || 'no build attestation'} · ${status} versus local collector`}
        style={{ color: status === 'match' ? '#42b883' : status === 'drift' ? '#f07878' : '#dfab50', whiteSpace: 'nowrap' }}>
        {peer.host} {status}
      </span>;
    })}
  </span>;
}
