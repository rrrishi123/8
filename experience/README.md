# experience/ — feel the 8 transports, watch the witness catch them

The interactive twin of the transport conformance tests. Nine transport pages +
a **witness wall** + a tiny **demo witness** server, so the whole thesis runs
standalone — no home, no omarchy, no build:

> one host fires a transport → the witness sees it automatically → here is the
> image and the clock (who fired what, and how soon 8 caught it).

## Run the demo (one command)

```bash
node serve.mjs          # http://localhost:8100   (PORT env optional)
```

Then:

1. Open **`/witness.html`** in one window — the reafference wall.
2. Open **`/`** (the index) in another; click any transport, fire it.
3. Watch the fire land on the wall in real time, stamped `caught +Nms`.

`serve.mjs` is a zero-dep Node server that both serves this folder **and** is a
minimal `8`:

| endpoint | role |
|---|---|
| `GET /health` | the CALL atom — liveness |
| `GET /feed` | the afferent CHANNEL (SSE) — the witness stream |
| `POST /fire` | a host reports it fired; the server stamps `seen`, broadcasts to `/feed` |

## The pages

| page | transport | live standalone? |
|---|---|---|
| `http.html` | http · CALL | ✅ (hits the demo witness `/health`) |
| `sse.html` | sse · CHANNEL afferent | ✅ (subscribes to `/feed`) |
| `websocket.html` | websocket · CHANNEL | ✅ (public echo) |
| `webrtc.html` | webrtc · CHANNEL | ✅ (same-page RTCPeerConnection loopback) |
| `mqtt.html` | mqtt · CHANNEL | ✅ (public broker via mqtt.js) |
| `grpc.html` | grpc · CALL/CHANNEL | frame-builder (browser can't speak raw grpc) |
| `unix.html` | unix · CALL/CHANNEL | explainer (browser can't dial a unix socket) |
| `mjpeg.html` | mjpeg · CHANNEL afferent | needs a real collector (point base at omarchy) |

Every page that fires calls `WIRE.fire(who, what, mode)` (`_wire.js`), which posts
to the witness — that is the "any host fires → 8 sees it" seam.

## Pointing at a real / deployed backend

The **target** for http/sse/mjpeg is swappable (index base field, stored in
`localStorage['wire.base']`): blank = this origin (the demo witness); or a live
omarchy collector (`http://<host>:7070`) for real screens and real BiDi frames.
Fires always report to the origin that served the page, so the witness is always
your local `8`.

## Deploy (always-up, home-decoupled)

`serve.mjs` maps cleanly to a **Cloudflare Worker + Durable Object** (the DO holds
the `clients` set for `/feed`), giving a free, always-up, home-independent public
demo. The static pages go on Cloudflare Pages; the base URL seam means the same
site can point at the Worker, a real wire, or a live omarchy — without touching
page code.
