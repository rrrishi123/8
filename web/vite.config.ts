import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
// The cockpit dev server runs on 8088 (same port we standardised on; it
// replaces the throwaway python static server). It talks to the collector
// at VITE_COLLECTOR_URL (default :7070).
// The browser-node registry is served by the COLLECTOR at /nodes (#1147) —
// joined to seats there, from ~/.8/browser-nodes.json + live docker — so a
// built cockpit (dist) keeps the registry. The dev-only vite plugin that used
// to serve /__8/browser-nodes.json is retired.
export default defineConfig({
  // #814: baked at DEV-SERVER START — the UI renders its age so a stale bundle
  // in a long-open tab is VISIBLE instead of silently NetworkErroring.
  define: { __BUILD_TS__: JSON.stringify(Date.now()) },
  plugins: [react()],
  server: { port: 8088, strictPort: true },
});
