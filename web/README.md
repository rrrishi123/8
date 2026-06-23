# web — the cockpit frontend

React in the **Wire** design system — 8's own, *not* Kosaten (Kosaten is a
separate, personal universe). Originated from a Claude Design prototype built on
Kosaten's tokens, re-based onto Wire when lifted here.

When importing the prototype here:

1. **Strip Claude Design's srcmap instrumentation** — the editor metadata
   wrapper (~144KB, not app logic). Keep the React app; re-base its tokens onto
   **Wire** (the prototype shipped Kosaten's CSS — that's the design-system seam to swap).
2. **Wire the data layer to the collector:**
   - afferent: subscribe to `GET /feed` (SSE) for the merged call + frame stream
   - efferent: `POST /run?session=ID` and `POST /broadcast` to act on the wire
3. **Replace the prototype's synthetic frames/rows** with the live feed. The
   viewport canvases take real `Page.startScreencast` / mjpeg frames per
   session, each sized to the device's true viewport.
4. **Eventually self-host** — serve the bundle from the collector and drop the
   unpkg CDN for production.

The leanness constraint is the *wire's*, not the frontend's: React is fine here
because it never touches http-mcp — it only speaks HTTP/SSE to the collector.
