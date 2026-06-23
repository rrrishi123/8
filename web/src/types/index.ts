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
