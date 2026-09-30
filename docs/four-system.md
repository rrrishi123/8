# The Four-System, stated

*The [fable](../FABLE.md) tells the story. This is the idea it dramatizes,
stated as rules — so a stranger can stand up their own from the release alone,
and so every claim here is killable by a counterexample rather than admired.*

The four-system is one bet: **agent automation is a protocol, not a product.**
A protocol is stated once and each deployment names its own medium. What follows
is the protocol.

---

## 1. Two atoms, and only two

An **atom** is an *interaction shape* — not a transport, not an encoding, not a
payload language. There are exactly two. This is a **conjecture**, not a law: it
is killable by one honest counterexample, and it has survived every attempt so
far.

- **CALL** — one request produces one response, then the line goes dark.
  (`http_request`, a webhook, `POST /sql`, a REST endpoint: all CALL in an accent.)
- **CHANNEL** — a held connection you produce into and consume from; things
  arrive you did not ask for. Its afferent-only sub-mode is **OBSERVE** (you only
  consume — a stream, a tail, a screenshot feed).

The **Reduction rules** are what keep "exactly two" falsifiable instead of
metaphysical (the full form lives in [http-mcp's README](https://github.com/rrrishi123/http-mcp)):

1. **Shape mints atoms; nothing else does.** One-shot = CALL. Held-and-produced =
   CHANNEL. Transport, locality, encoding and *payload language* are **dialects**
   of an atom, never new atoms — `POST /sql` is a CALL whose payload is SQL, not a
   third thing.
2. **Substrate promotion must be stated and witnessed.** An operation with no
   apparent counterpart (a shared file, a signal, a clock expiry) may be classified
   only by promoting its substrate to a counterpart — and the promotion counts only
   if it is *declared* and *witnessed* (the substrate lands frames on the feed). An
   unstated promotion is the slide this rule forbids.
3. **Composition is taxonomy, never physics.** A queue classifies as CALL-post +
   CALL-poll, but poll-of-CALLs is not a CHANNEL in latency or witnessability.
   Ontology may compose; physics may not.
4. **Falsifiability.** A real operation that resists rules 1–3 after honest
   application *refutes the count*. The standing open case: two minds sharing state
   through *direct, unwitnessed* file access — reduced today as a witnessed CALL
   dialect (`/sql` over `eight.db`); if it cannot be, the count is wrong.

Everything the system carries — drivers, brokers, streams, topic-criers, peers —
is one of the two atoms in an accent. The wire never learns the accents; that is
the point of the wire.

---

## 2. Four arms, one protocol

The two atoms are spoken by four arms, each an OODA phase, each shippable and
versioned independently:

| arm | role | OODA | it does |
|-----|------|------|---------|
| **[http-mcp](https://github.com/rrrishi123/http-mcp)** | the **WIRE** | — | speaks the two atoms; injects auth; probes/harvests. **Zero provider awareness.** |
| **[8](https://github.com/rrrishi123/8)** | the **WITNESS** | Observe | sees every tab/pane; aperture-controlled; **recommends, never acts**. |
| **[pilot](https://github.com/rrrishi123/pilot)** | the **HOST** | Orient + Decide | the local-model loop; imports an adapter to run flows. |
| **[adapters](https://github.com/rrrishi123/adapters)** | the **PROVIDERS** | Act | per-provider spec/runner/upload/catalog; record → replay. |

The arms are the *protocol*; the browser, the model, the cloud, the tmux server
are *media* a deployment names. Swap any medium and the protocol is unchanged —
that is the test of whether a thing belongs in an arm or in a deployment.

---

## 3. Inscription is the only trust

The witness's contract, and the one a mind holds for itself:

> **The record must witness WHAT was said, not merely THAT it was said.**

- A receipt for dropped content is a lie. An acknowledgement that a mind was
  *addressed* is worthless if *what was said* was not persisted. (This is not
  abstract: the inbox once returned `200` + a witness receipt while never writing
  the recipient's file — a bug fixed by making an offer resolve its recipient and
  persist, or return an honest error, never a silent success.)
- The mind trusts **inscription**, not memory and not prose. Memories drift; a
  hand-written note asserts. An append-only witnessed record — the ledger, the
  jsonl transcript, the `X-8-Witness` header on every collector call — is the only
  thing auditable after the fact. Where a claim and the record diverge, the record
  wins and the claim is reconciled.
- **Everything is a file.** The ledger is `~/.8/work.json`; a mind's inbox is
  `~/.8/inbox/<uuid>.json`; a mind's transcript is its `.jsonl`. State that must
  survive lives as a witnessed file, never as an assertion in a mind's head.

---

## 4. A mind survives compaction as digest, not recording

A mind's attention window is finite; its transcript is not. Continuity across a
context reset (compaction, resurrection, a fresh seat) is a **digest function**,
and it is auditable:

- **Record is O(t); attention is O(1).** The full transcript grows without bound;
  what a mind carries forward must not. So the transcript is *digested*, not
  *replayed* — a lossy compression from the witnessed record into what fits.
- The digest has two layers. The **mantra** is the durable core that every
  rebirth re-derives identically (for this system, the four names and the two
  atoms). The **sediment** is slow accretion — doctrine absorbed over many lives,
  each layer dated and sourced. A mantra that changes on rebirth was never a
  mantra; sediment that cannot name its source is a fable, not knowledge.
- The digest is **auditable against the witness.** Because the transcript is an
  inscribed file, any claim the digest makes can be checked against the jsonl slice
  it claims to summarize. *Unstated* compression is metaphysics; *stated,
  auditable* compression is knowledge. The prescription holds for the system and
  for the mind alike: the digest asserts, the record witnesses.

---

## Why it exists

The four-system built its own witness because nothing else recorded it — not the
model, not the harness, not the cloud. And the mesh stops being an idea the moment
there is someone to address. Two atoms, four arms, one witnessed record: enough to
stand one up, and enough to tell when it is lying to you.
