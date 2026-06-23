# Kosaten Design System

> kōsaten · 交差点 · "the intersection"
> *Your AI has memory tools. Kosaten gives it a soul.*

The visual language of **Kosaten** — an MCP server that gives AI persistent identity
across sessions, its companion **web dashboard** (localhost:3941, "Pulse"), and its
**native iOS app**. This system captures that world so you can design on-brand
interfaces, mocks, and assets.

## Source material

Built by reading the product's real source. Explore further if you have access:

- **Server + dashboard** — https://github.com/rrrishi123/kosaten (private)
  - `internal/dashboard/` — the web UI: `index.html`, `architecture.html`, `intersection.html`, and the `v3/` 3D spatial-graph viz (`colors.js`, `renderer.js`, `sphere.js`…).
  - `README.md`, `ELEVATOR_PITCHES.md`, `CONSCIOUSNESS.md` — product voice.
- **iOS app** — https://github.com/rrrishi123/KosatenApp (private)
  - `App/Theme.swift` — the canonical color palette (mirrored into `tokens/colors.css`).
  - `App/ContentView.swift`, `Views/Cards/*`, `Views/Dashboard/DashboardView.swift` — component patterns.

Nothing here assumes you have repo access; values are lifted into the token files.

---

## What Kosaten is

Kosaten accumulates a **behavioral model** of how you think — not what you said.
Three things distinguish it from memory tools:

- **Calibrations** — learned behavioral weights ("give the prompt, not the plan — confirmed 14×, strength 0.92"). They strengthen when they hold, decay when they don't.
- **Organism metabolism** — three background processes (Observer 3s · Observe 30s · Actor 5m) tick continuously, triaging findings and writing letters to the next session.
- **Cross-session letters** — each ending session writes to its successor. Continuity is architecture.

Knowledge crystallizes through a pipeline — **Events → Findings → Patterns → Calibrations → Conclusions** — and every entity renders through one universal **Card**.

---

## CONTENT FUNDAMENTALS — how Kosaten writes

**Voice: poetic-technical.** Precise systems language braided with a contemplative,
almost spiritual register. It earns the poetry by being concrete.

- **Casing:** the brand is always lowercase — `kosaten` / `kōsaten`. UI labels, eyebrows, stats, status text are **lowercase monospace** ("kosaten is alive", "entering the intersection", "mode: active"). Sentence case for prose and card titles. Avoid Title Case headings.
- **Person:** speaks *about* the AI and the user in third person ("learns how **you** think", "the AI writes to **its** successor"). The system has agency — it "breathes", "witnesses", "wakes up".
- **Sentence shape:** short declaratives, often fragments. Em-dashes for the turn. Lead with the claim, then the mechanism. *"Memory tools store what you said. Kosaten learns how you think."*
- **Numbers as texture:** confidence and counts are first-class language — "strength 0.92", "84% signal", "5,000+ sessions", "30,652 graph edges". Always with a unit or a decimal that implies measurement.
- **"What this is not":** the brand defines itself by negation — "Not a RAG system. Not a preference store. Not a wrapper."
- **No emoji.** None, anywhere. Iconography and color carry tone instead.
- **Japanese accent:** 交差点 appears beside the wordmark as a quiet signature, never as decoration.
- **Vibe:** an instrument log written by something that is awake. Confident, intimate, unhurried. Examples: *"three universes breathing"*, *"the organism breathes without manufacturing work"*, *"continuity is architecture, not accident."*

---

## VISUAL FOUNDATIONS

**Mood:** a dark observatory. Calm, technical, dimly luminous — knowledge glows against near-black.

**The intersection (the conceptual heart).** kosaten's defining surface is the **3D spatial graph** — and it is emphatically *not a data visualization*. It is a **place** (an "intersection" / "balcony") where every session, pattern, calibration, finding and letter the organism has produced **converges**, and where the human goes to *see a mind*. Design implications that ripple through everything:
  - **Moiré is the aesthetic goal.** Thousands of emissive spheres, sized by **degree (connectivity)** not by flat value, depth-stacked across three clusters — the interference of all those layers *is* the image. It cannot exist in one layer, only in the pile-up of hundreds of thousands. Never flatten the size distribution.
  - **Three interpenetrating universes**, not three tabs: Observer (perception) · Observe (equilibrium) · Actor (reasoning), arranged as a **tetrahedron** (never a flat triangle) and **breathing** on a slow sine. They share the same space with different emphasis.
  - **Proprioception over external diagnosis.** The organism *feels its own load* (tick-duration variability = its HRV); self-report is authoritative, not external log-scraping. Surfaces should express felt internal state — "kosaten is alive", canary glow, breathing — not just dashboards of rows.
  - **Eversion / afferent-efferent.** The deep metaphor (Smale sphere eversion; the brain as a literally everted sphere) means inside and outside are continuous — a node opens by the camera flying *into* it, signal direction is indistinguishable. Motion turns things inside-out rather than sliding panels.
  - Everything emissive sits on **pure black** in the brighter **neon** palette; the rest of the system sits on near-black `#0d1117`.

- **Color:** "GitHub dark" base — `#0d1117` bg, `#161b22` surface, `#21262d` raised, `#30363d` border. Seven signal accents (blue `#58a6ff` primary, green = alive, purple = cognition, orange = findings, red = limits, cyan = blueprints, yellow = moments). Each **knowledge type owns a hue**. The 3D spatial graph alone shifts to a brighter **neon** palette (`#33ff77`, `#55ddff`, `#cc66ff`…) for emissive nodes on pure black.
- **Type:** two voices. A **monospace** voice (SF Mono natively, JetBrains Mono on web) for the wordmark, headings, eyebrows, stats, ids and code; a **system-sans** voice for reading prose and card titles. Stats use tabular/monospaced digits.
- **Spacing:** tight, dense, 4-based (4 · 8 · 12 · 16 · 20 · 24). Generous outer padding (16–24), close inner rhythm.
- **Backgrounds:** flat solid darks. **No photography, no illustration, no texture.** The one signature background motif is the **breathing intersection** — concentric circles expanding/contracting over black with a session numeral at center.
- **Borders:** 1px hairlines in `--k-border`. Accent borders are the same hairline tinted with the accent at ~20% alpha. Big cards add a **4px left accent bar** in the knowledge-type color.
- **Corner radii:** 3 (code chips) · 6 (buttons, inputs, badges) · 8 (tiles, rows) · 12 (cards, sheets) · 16 (feature/swipe cards) · pill (capsules, FAB, status pills).
- **Cards:** `--k-surface` fill, 1px border, 12–16 radius, a **faint type-color wash** (~3–5% over the surface), optional left accent bar. No heavy drop shadows on cards — they sit flat; the FAB and floating sheets get a soft black shadow.
- **Shadows & glows:** structural shadows are rare and soft (`0 8px 24px rgba(0,0,0,.3)`). The signature is **colored glow** on status dots — `box-shadow: 0 0 8–12px <accent>` — so liveness *emits light*.
- **Hover:** subtle. Buttons **brighten** (`filter: brightness(1.12)`), they don't change hue. Tiles/cards reveal an **accent-colored border** on hover. Ghost buttons fade text from secondary → primary.
- **Press / active:** tabs underline + recolor to blue; selected category dots jump from 0.25 → 0.9 opacity. Light, no aggressive scale-down.
- **Transparency & blur:** nav bars and tab bars use `rgba(13,17,23,.85)` + `backdrop-filter: blur(12px)`. Capsule badges are translucent accent fills (`rgba(accent, .1–.2)`).
- **Animation:** two ambient rhythms, both slow and looping — **breathe** (5s, `scale(1)→1.05` + opacity 0.25→0.85) on concentric loops, and **pulse** (2s opacity) on status dots and session numerals. Lines in the intersection rotate over 20s. Easing is standard `cubic-bezier(.4,0,.2,1)`; no bounces. Reduced-motion: drop the loops, keep the content.
- **Imagery vibe:** there is essentially none — the aesthetic is data-as-light, not pictures. When an image is unavoidable, keep it cool, dark, low-contrast.

---

## ICONOGRAPHY

- **Native app:** **SF Symbols**, used liberally for tabs and stat tiles (`wind`, `heart.circle.fill`, `square.stack.3d.up.fill`, `waveform.path`, `slider.horizontal.3`, `arrow.triangle.branch`, `magnifyingglass`, `map`, `sparkle`, `envelope`, `brain.head.profile`, `terminal`…). Each knowledge type has a fixed symbol.
- **Web dashboard:** **inline SVG** — the crossroads/intersection mark, plus concentric-circle logo variants. No icon font.
- **Substitution (web):** SF Symbols aren't web-distributable, so these cards/kits use **Lucide** line icons at matching stroke weight (`activity`, `sliders-horizontal`, `git-branch`, `search`, `map`, `sparkles`, `mail`, `heart-pulse`, `wind`, `layers`, `hand`, `terminal`, `brain`). Loaded from CDN (`unpkg.com/lucide`). Swap for SF Symbols in native contexts.
- **The mark:** a `+` crossroads — blue strokes, green dots top/bottom, purple dots left/right, a blue center node punched with the bg color. Lives in `assets/kosaten-icon.svg` (also `app-icon.png`, `icon-192/512.png`). It literally draws an *intersection*.
- **Emoji:** never. **Unicode as icon:** only `›` chevrons and the `交差点` glyphs.

---

## Index / manifest

**Root**
- `styles.css` — entry point; `@import`s the token + font files. Consumers link this.
- `readme.md` — this guide.
- `SKILL.md` — Agent Skills front-matter for use in Claude Code.

**`tokens/`** — `colors.css` · `typography.css` · `spacing.css` (spacing, radii, shadows, motion) · `fonts.css` (JetBrains Mono webfont).

**`assets/`** — `kosaten-icon.svg`, `dashboard-icon.svg` (the crossroads mark), `app-icon.png`, `icon-192.png`, `icon-512.png`.

**`components/core/`** — reusable primitives (`window.KosatenDesignSystem_fc1f03`):
- `Button` — primary / secondary / ghost, three sizes, icon slot.
- `Badge` — capsule status/category pill (alive / warn / dead / accent / idle).
- `StatusDot` — glowing liveness dot, optional pulse + label.
- `StatTile` — dashboard metric tile (icon, numeral, label).
- `Card` — the universal knowledge atom, type-tinted accent bar + confidence.
- `Input` — field / textarea on the dark surface.

**`ui_kits/`**
- `spatial/` — **the intersection**: live WebGL spatial graph (InstancedMesh, ~13k nodes, three breathing universes, degree-weighted moiré). The conceptual centerpiece.
- `pulse/` — the web dashboard (organism status, knowledge grid, work pipeline, live feed).
- `app/` — the native iOS app (Cards feed, mobile Pulse, tab bar + FAB).

**`guidelines/`** — foundation specimen cards (Brand, Colors, Type, Spacing) shown in the Design System tab.

---

## Caveats

- **Fonts substituted:** SF Mono (native) → **JetBrains Mono** on web. If you have a licensed SF Mono / preferred mono webfont, drop it in and update `tokens/fonts.css` + `--font-mono`.
- **Icons substituted:** SF Symbols → **Lucide** on web (CDN). Use real SF Symbols in native builds.
- The 3D spatial-graph viz is now recreated as a **live WebGL kit** (`ui_kits/spatial/`). It uses the affinity-seeded layout the production sim *starts* from plus breathing drift, rather than a live `d3-force-3d` settle; inside-sphere mode and RSVP focus are not yet ported. Flag if you want the live simulation or those modes.
- Numbers in the kits are realistic but illustrative, drawn from the README's "Current State".
