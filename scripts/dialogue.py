#!/usr/bin/env python3
"""dialogue.py — #1233 [T11]: programmatic N-tab CDP dialogue over the witnessed wire.

Two browser tabs take turns with ZERO human interaction:
  CALL    = write text into a tab's input + press Enter  (POST /act → Input.insertText + Enter)
  CHANNEL = subscribe to that tab's reply-complete on the T5 bus (GET /event?tab=&kind=&since=)

The reply's TEXT now rides the bus as event.tail (#1233 collector change), so the loop CARRIES
tab A's settled reply into tab B and sends it — one round = A→B (→A …), every hop inscribed on
the ledger. 1-mind→N-tabs and N-minds→N-tabs are the same primitive; this validates it single-host.

  python3 dialogue.py --open-echo --seed hi --rounds 1   # spins up two deterministic echo tabs
  python3 dialogue.py --a <ctxA> --b <ctxB> --seed hi     # drive two existing chat tabs instead

8 NEVER initiates (handleAct): this orchestrator lives OUTSIDE the collector and only drives it
through the witnessed endpoints — /watch, /act, /event.
"""
import argparse, json, sys, time, urllib.parse, urllib.request

COLLECTOR = "http://127.0.0.1:7070"
CDP = "http://127.0.0.1:9333"

# A deterministic echo testbed: an <input> whose Enter appends "echo: <text>" to a <div>, so
# document.body.innerText shifts then settles — exactly what the substrate-agnostic watcher
# (cdpwatch) reads to emit reply-complete. Pre-encoded (proven to open via CDP /json/new).
ECHO_Q = ("data:text/html,%3Cmeta%20charset=utf-8%3E%3Cbody%3E%3Cinput%20id=i%20autofocus%3E"
          "%3Cdiv%20id=log%3E%3C/div%3E%3Cscript%3Evar%20i=document.getElementById('i');"
          "i.addEventListener('keydown',function(e){if(e.key==='Enter'){var%20d=document."
          "createElement('div');d.textContent='echo:%20'+i.value;document.getElementById('log')"
          ".appendChild(d);i.value='';i.focus();}});%3C/script%3E")


def jreq(url, method="GET", body=None, actor="dialogue"):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(url, data=data, method=method,
                               headers={"Content-Type": "application/json", "X-8-Actor": actor})
    with urllib.request.urlopen(r, timeout=25) as f:
        raw = f.read()
        return json.loads(raw) if raw else {}


def open_echo():
    return jreq(f"{CDP}/json/new?{ECHO_Q}", method="PUT")["id"]


def watch(ctx, substrate):
    jreq(f"{COLLECTOR}/watch?context={ctx}&substrate={substrate}", method="POST")


def cursor():
    return jreq(f"{COLLECTOR}/event?limit=1").get("seq", 0)


def activate(ctx):
    # Bring the tab to the front so the renderer has focus — Input.insertText only
    # populates an editable on a focused renderer; a background tab drops the text.
    try:
        urllib.request.urlopen(urllib.request.Request(f"{CDP}/json/activate/{ctx}", method="GET"), timeout=10).read()
    except Exception:
        pass


def write_send(ctx, substrate, text):
    activate(ctx)
    time.sleep(0.2)
    # Click the input to focus the editable, then type + Enter.
    jreq(f"{COLLECTOR}/act?session={substrate}", "POST", {"context": ctx, "action": "click", "x": 100, "y": 18})
    time.sleep(0.2)
    jreq(f"{COLLECTOR}/act?session={substrate}", "POST", {"context": ctx, "action": "type", "text": text})
    time.sleep(0.25)
    jreq(f"{COLLECTOR}/act?session={substrate}", "POST", {"context": ctx, "action": "press", "text": "Enter"})


def await_reply(ctx, since, timeout=45):
    t0 = time.time()
    while time.time() - t0 < timeout:
        evs = jreq(f"{COLLECTOR}/event?tab={ctx}&kind=reply-complete&since={since}").get("events", [])
        if evs:
            return evs[-1]
        time.sleep(1.5)
    return None


def turn(ctx, substrate, text, label):
    since = cursor()
    print(f"  → {label}: write+send {text!r}")
    write_send(ctx, substrate, text)
    ev = await_reply(ctx, since)
    if not ev:
        print(f"  ✗ {label}: no reply-complete within timeout")
        sys.exit(2)
    tail = (ev.get("tail") or "").strip()
    print(f"  ← {label}: reply-complete id={ev['id']} tail={tail[-80:]!r}")
    return tail


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--a")
    ap.add_argument("--b")
    ap.add_argument("--seed", default="hello")
    ap.add_argument("--substrate", default="chrome")
    ap.add_argument("--rounds", type=int, default=1)
    ap.add_argument("--open-echo", action="store_true")
    a = ap.parse_args()

    if a.open_echo:
        a.a, a.b = open_echo(), open_echo()
        print(f"opened echo tabs  A={a.a}  B={a.b}")
        time.sleep(2.0)  # let the manifest reconcile the new tabs from chrome
    if not (a.a and a.b):
        sys.exit("need --a and --b (or --open-echo)")

    watch(a.a, a.substrate)
    watch(a.b, a.substrate)
    time.sleep(3.0)  # one baseline read before the first event (no false first settle)

    msg = a.seed
    for n in range(a.rounds):
        print(f"round {n + 1}/{a.rounds}")
        reply_a = turn(a.a, a.substrate, msg, "A")
        reply_b = turn(a.b, a.substrate, reply_a, "B")
        msg = reply_b  # carry B's reply back to A for the next round
    print("✓ dialogue complete — one round A→B inscribed on the T5 bus, zero human interaction")


if __name__ == "__main__":
    main()
