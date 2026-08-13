#!/usr/bin/env python3
"""Frame Zero pipeline - driven through the wire (four-arm system).
Modes: ingest (record job ids/urls), download (fetch mp4s), stitch (ffmpeg concat).
State persists in /home/claude/wire/pipeline_state.json across runs."""
import json, os, subprocess, sys, threading, http.server, time

WIRE = "/Users/rishirajs/Desktop/repos/http-mcp/http-mcp"
STATE_F = "/Users/rishirajs/Desktop/repos/8/higgsfield/pipeline_state.json"
MANIFEST_F = "/Users/rishirajs/Desktop/repos/8/higgsfield/frame_zero_v2.json"
CLIPS = "/Users/rishirajs/Desktop/repos/8/higgsfield/clips"
os.makedirs(CLIPS, exist_ok=True)

def load(f, d): return json.load(open(f)) if os.path.exists(f) else d
state = load(STATE_F, {"shots": {}})
manifest = load(MANIFEST_F, {"shots": []})

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self,*a): pass
    def _s(self,o,c=200):
        b=json.dumps(o).encode(); self.send_response(c); self.send_header("Content-Type","application/json"); self.end_headers(); self.wfile.write(b)
    def do_GET(self):
        if self.path=="/state": self._s(state)
        elif self.path.startswith("/download/"):
            sid=self.path.split("/")[-1]; sh=state["shots"].get(sid)
            if not sh or not sh.get("url"): self._s({"err":"no url"},404); return
            out=f"{CLIPS}/{sid}.mp4"
            r=subprocess.run(["curl","-sS","-f","-o",out,"--max-time","120",sh["url"]],capture_output=True,text=True)
            if r.returncode==0 and os.path.getsize(out)>1000:
                sh["downloaded"]=True; self._s({"ok":sid,"bytes":os.path.getsize(out)})
            else:
                sh["downloaded"]=False; self._s({"err":"download failed (403 likely = CDN not in allowed domains)","detail":r.stderr[-200:]},502)
        elif self.path=="/stitch":
            ready=[s["id"] for s in manifest["shots"] if state["shots"].get(s["id"],{}).get("downloaded")]
            if not ready: self._s({"err":"nothing downloaded"},400); return
            lst="\n".join(f"file '{CLIPS}/{i}.mp4'" for i in ready)
            open(f"{CLIPS}/list.txt","w").write(lst)
            out="/Users/rishirajs/Desktop/repos/8/higgsfield/frame-zero-cut.mp4"
            r=subprocess.run(["ffmpeg","-y","-f","concat","-safe","0","-i",f"{CLIPS}/list.txt","-c:v","libx264","-preset","fast","-crf","20","-c:a","aac",out],capture_output=True,text=True)
            ok = r.returncode==0
            self._s({"ok":ok,"stitched":ready,"out":out if ok else None,"err":None if ok else r.stderr[-300:]})
        else: self._s({"err":"?"},404)
    def do_POST(self):
        body=json.loads(self.rfile.read(int(self.headers.get("Content-Length",0)) or b"{}"))
        if self.path=="/ingest":
            sid=body["id"]; state["shots"].setdefault(sid,{}).update(body); self._s({"ok":sid})
        else: self._s({"err":"?"},404)

def via_wire(calls):
    msgs=[{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"pipeline","version":"2"}}}]
    for i,(m,u,b) in enumerate(calls):
        a={"method":m,"url":u}; a.update({"body":json.dumps(b)} if b else {})
        msgs.append({"jsonrpc":"2.0","id":i+1,"method":"tools/call","params":{"name":"http_request","arguments":a}})
    out=subprocess.run([WIRE],input="\n".join(json.dumps(m) for m in msgs),capture_output=True,text=True,timeout=300).stdout
    res=[]
    for line in out.strip().split("\n"):
        r=json.loads(line)
        if isinstance(r.get("id"),int) and r["id"]>=1:
            res.append(json.loads(r["result"]["content"][0]["text"]))
    return res

if __name__=="__main__":
    srv=http.server.HTTPServer(("127.0.0.1",8813),H)
    threading.Thread(target=srv.serve_forever,daemon=True).start(); time.sleep(0.4)
    cmd=sys.argv[1] if len(sys.argv)>1 else "state"
    if cmd=="ingest":
        jobs=json.loads(sys.argv[2])
        calls=[("POST","http://127.0.0.1:8813/ingest",j) for j in jobs]
        print(via_wire(calls))
    elif cmd=="download":
        ids=[s["id"] for s in manifest["shots"] if state["shots"].get(s["id"],{}).get("url")]
        calls=[("GET",f"http://127.0.0.1:8813/download/{i}",None) for i in ids]
        for r in via_wire(calls): print(r.get("status"), r.get("body","")[:160])
    elif cmd=="stitch":
        print(via_wire([("GET","http://127.0.0.1:8813/stitch",None)]))
    else:
        print(json.dumps(state,indent=1))
    json.dump(state,open(STATE_F,"w"),indent=1)
