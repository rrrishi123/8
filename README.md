# 8

An operator's inspector for live browser- and device-automation sessions. Like
Appium Inspector, but not tied to a single device or protocol: it watches every
live session at once — WebDriver/Appium over HTTP and CDP/WebDriver-BiDi over a
WebSocket — and lets you inspect and drive any of them from one place.

It sits above the automation layer and owns no protocol logic of its own. It
talks HTTP/SSE to a small collector service, which fans in from the running
sessions; keeping the protocol layer untouched keeps the whole thing lean.

## Layout

```
web/        cockpit UI (React + Vite + TypeScript)
collector/  capture + orchestration service (Go, stdlib only)
```

The collector unifies every session's traffic into one SSE feed (`/feed`),
serves the live session list (`/sessions`), and routes commands to a session
(`/run`, `/act`, `/shot`, `/source`). The cockpit is protocol-blind — it reads
those endpoints and renders.

## What it does today

- **Capture stream** — every call/frame as a row (physics, method, route,
  session), from real traffic only.
- **Session rail** — live sessions, filterable, auto-registered via a shared
  `~/.8/sessions.json`, with dead ones pruned.
- **Interaction surface** — click a session for an Appium-Inspector-style view:
  the device/page screen at real size, an element-bounds overlay, the source
  tree, and a selected-element panel with locators and tap — all over the HTTP
  wire (`/shot`, `/source`, `/act`).

## Running

```
# collector
cd collector && go run .              # serves :7070

# cockpit
cd web && npm install && npm run dev  # serves :8088, talks to :7070
```

## Roadmap

- Per-session traffic: the collector echoes its own outbound calls into the feed
  so they show in the stream, tagged by session.
- Dynamic command palette: enumerate what a session's wire actually supports
  (Appium extensions by `automationName` + the static W3C/BiDi command sets) and
  render them as triggerable forms; native/webview context switch as a control.
- Live streaming transport (scrcpy/MJPEG for devices, CDP screencast for
  browsers) in place of polled screenshots.
- Further wire types as they appear (webhooks/callbacks, etc.).

## License

MIT.
