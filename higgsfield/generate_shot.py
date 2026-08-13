#!/usr/bin/env python3
"""generate_shot.py — fire ONE higgsfield shot through the wire, extend-chained.

Usage:  python3 generate_shot.py <PREV_JOB_ID> <PROMPT_FILE>
  e.g.  python3 generate_shot.py c9090b7e /tmp/p03_extend.txt   (P03 extends P02)

The proven sequence (all via http-mcp / BiDi, browser-Unlimited = 0 credit):
  1. discover the higgsfield /ai/video tab context
  2. Extend Video mode -> Add-video-to-extend -> Video Generations tab
  3. Select {PREV_JOB_ID}  (the frame-zero chain: this shot continues the last)
  4. paste the prompt via system clipboard + real Cmd+V (React editor ignores JS insert)
  5. real pointer-click Generate Unlimited
Then %37 polls higgsfield MCP -> downloads -> re-stitches. ONE generation at a time.
"""
import json, subprocess, sys, time, base64

WIRE = "/Users/rishirajs/Desktop/repos/http-mcp/http-mcp"
WS = "ws://127.0.0.1:9222/session/8210e817-5982-4a95-a7bf-581f9b2e59e0"

def wire(method, params):
    args = {"ws_url": WS, "method": method, "params": params}
    msgs = [
        {"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"gen","version":"1"}}},
        {"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"bidi_command","arguments":args}},
    ]
    out = subprocess.run([WIRE], input="\n".join(json.dumps(m) for m in msgs),
                         capture_output=True, text=True, timeout=60).stdout
    for line in out.strip().split("\n"):
        try:
            r = json.loads(line)
            if r.get("id") == 1:
                return json.loads(r["result"]["content"][0]["text"])
        except Exception:
            pass
    return {}

def evaljs(ctx, expr):
    r = wire("script.evaluate", {"expression": expr, "target": {"context": ctx}, "awaitPromise": True})
    return (((r.get("response") or {}).get("result") or {}).get("result") or {}).get("value")

def click_xy(ctx, x, y):
    wire("input.performActions", {"context": ctx, "actions": [
        {"type":"pointer","id":"m","actions":[
            {"type":"pointerMove","x":int(x),"y":int(y)},
            {"type":"pointerDown","button":0},{"type":"pause","duration":70},
            {"type":"pointerUp","button":0}]}]})

def coords(ctx, sel_js):
    v = evaljs(ctx, "(function(){var e=%s; if(!e) return ''; var r=e.getBoundingClientRect(); return Math.round(r.left+r.width/2)+','+Math.round(r.top+Math.min(r.height/2,18));})()" % sel_js)
    return [int(n) for n in v.split(",")] if v else None

def find_ctx():
    r = wire("browsingContext.getTree", {})
    def walk(ns):
        for n in ns:
            if "higgsfield.ai/ai/video" in (n.get("url") or ""):
                return n["context"]
            c = walk(n.get("children") or [])
            if c: return c
    return walk((r.get("response") or r).get("result", {}).get("contexts", []))

def main():
    prev_job, prompt_file = sys.argv[1], sys.argv[2]
    prompt = open(prompt_file).read().strip()
    ctx = find_ctx()
    assert ctx, "higgsfield /ai/video tab not found"
    print("ctx", ctx)
    # 2. ensure Extend Video + open the source picker
    evaljs(ctx, "(function(){var b=[...document.querySelectorAll('button,div,span')].find(e=>/^Extend Video$/.test((e.innerText||'').trim())); if(b)(b.closest('button,[role=button]')||b).click(); return 1;})()")
    time.sleep(1)
    evaljs(ctx, "(function(){var z=[...document.querySelectorAll('button,div,label')].find(e=>/add video to extend|up to 3s/i.test((e.innerText||'').trim())); if(z)(z.closest('button,[role=button],label')||z).click(); return 1;})()")
    time.sleep(1.5)
    # 3. Video Generations tab, then Select {prev_job}
    evaljs(ctx, "(function(){var t=[...document.querySelectorAll('button,div,span,[role=tab]')].find(e=>/^Video Generations$/i.test((e.innerText||'').trim())); if(t)(t.closest('button,[role=tab],[role=button]')||t).click(); return 1;})()")
    time.sleep(2)
    sel = evaljs(ctx, "(function(){var b=[...document.querySelectorAll('button')].find(e=>((e.getAttribute('aria-label')||'').indexOf('Select '+%s)===0)); if(!b) return 'NO_SEL'; b.click(); return 'SELECTED';})()" % json.dumps(prev_job))
    print("select:", sel)
    time.sleep(2)
    # 4. paste prompt via clipboard
    subprocess.run(["pbcopy"], input=prompt, text=True)
    fld = coords(ctx, "document.querySelector('[contenteditable=true]')")
    if fld: click_xy(ctx, *fld); time.sleep(0.6)
    wire("input.performActions", {"context": ctx, "actions": [
        {"type":"key","id":"k","actions":[
            {"type":"keyDown","value":""},{"type":"keyDown","value":"v"},
            {"type":"keyUp","value":"v"},{"type":"keyUp","value":""}]}]})
    time.sleep(1)
    got = evaljs(ctx, "(function(){var e=document.querySelector('[contenteditable=true]'); return e?e.innerText.length:0;})()")
    print("prompt chars:", got)
    assert got and int(got) > 20, "prompt did not paste"
    # 5. Generate Unlimited
    gen = coords(ctx, "[...document.querySelectorAll('button')].find(e=>/generate/i.test(e.innerText)&&/unlimited/i.test(e.innerText))")
    assert gen, "Generate button not found"
    click_xy(ctx, *gen)
    print("GENERATE fired at", gen, "— now %37 polls -> downloads -> stitches")

if __name__ == "__main__":
    main()
