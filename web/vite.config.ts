import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import { readFile } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

// The cockpit dev server runs on 8088 (same port we standardised on; it
// replaces the throwaway python static server). It talks to the collector
// at VITE_COLLECTOR_URL (default :7070).

// eightNodes — serve the browser-node registry (~/.8/browser-nodes.json, or
// $EIGHT_HOME/browser-nodes.json) to the cockpit at /__8/browser-nodes.json.
// Read per request (no cache) so browser-nodes-sync.sh output is live. Missing
// file → an empty registry, never an error: the canvas then derives hosts from
// the collector's seats alone.
function eightNodes(): Plugin {
  return {
    name: 'eight-nodes',
    configureServer(server) {
      server.middlewares.use('/__8/browser-nodes.json', (_req, res) => {
        const home = process.env.EIGHT_HOME || join(homedir(), '.8');
        readFile(join(home, 'browser-nodes.json'), (err, buf) => {
          res.setHeader('content-type', 'application/json');
          res.setHeader('cache-control', 'no-store');
          res.end(err ? '{"nodes":[]}' : buf);
        });
      });
    },
  };
}

export default defineConfig({
  // #814: baked at DEV-SERVER START — the UI renders its age so a stale bundle
  // in a long-open tab is VISIBLE instead of silently NetworkErroring.
  define: { __BUILD_TS__: JSON.stringify(Date.now()) },
  plugins: [react(), eightNodes()],
  server: { port: 8088, strictPort: true },
});
