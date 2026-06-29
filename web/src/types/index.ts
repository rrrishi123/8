// The contract with the collector (cmd/collector). Real data only — no
// placeholder sessions, no synthetic rows.

export interface FeedEvent {
  session: string;
  origin?: 'BIDI' | 'COLLECTOR';
  physics?: 'call' | 'channel';
  frame: {
    method?: string;
    params?: Record<string, unknown>;
    [k: string]: unknown;
  };
}

export interface Session {
  id: string;
  hub: string;
  kind: string; // local | cloud
  physics: 'call' | 'channel';
  stream?: string; // live MJPEG source, if the session has one
  status?: 'live' | 'disconnected'; // disconnected = tombstone (device/socket gone)
}

export interface CaptureRow {
  id: number;
  origin: 'BIDI' | 'COLLECTOR';
  physics: 'channel' | 'call';
  session: string;
  method: string; // BiDi method, or HTTP method for echoed calls
  detail: string; // url / route
  at: number;
  raw: unknown; // the full frame payload, for the inspector
  tab?: string; // source browsing context — which tab on the shared Firefox channel
  shot?: { session: string; context?: string }; // what to screenshot when this row is clicked
  ledgerId?: number; // links a row (8's own call) to its full ledger record → replay
  latencyMs?: number; // round-trip latency, if known
}

export interface ParsedCurl {
  method: string;
  url: string;
  headers: Record<string, string>;
  body?: string;
}

export interface FetchResult {
  status: number;
  latency_ms: number;
  headers: Record<string, string>;
  body: string;
}

// Benchmark batches + the request ledger — 8 as the witness: clubbed + replayable.
export interface Stat {
  n: number; min: number; p50: number; p90: number; p99: number; p999: number; max: number; mean: number;
}

export interface BenchRec {
  id: number; ts: string; tag?: string; session: string; n: number;
  read: Stat; see: Stat;
  equiv_p50: number; equiv_p99: number; byte_ratio: number;
  read_bytes: number; see_bytes: number; read_err: number; see_err: number;
}

export interface ReqRec {
  id: number; ts: string; physics: 'call' | 'channel'; session?: string;
  method: string; url: string; headers?: Record<string, string>; body?: string;
  status: number; latency_us: number; resp_bytes: number; resp_preview?: string;
  replayable: boolean;
}

export interface ReplayResult {
  replayed?: number; new_id?: number; status?: number; latency_us?: number;
  resp_bytes?: number; response?: string; error?: string;
}
