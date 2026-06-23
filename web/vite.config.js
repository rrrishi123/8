import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
// The cockpit dev server runs on 8088 (same port we standardised on; it
// replaces the throwaway python static server). It talks to the collector
// at VITE_COLLECTOR_URL (default :7070).
export default defineConfig({
    plugins: [react()],
    server: { port: 8088, strictPort: true },
});
