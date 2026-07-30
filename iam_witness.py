#!/usr/bin/env python3
"""8 — the WITNESS, IAM source. Read-only. Observes the stage IAM pool via the adaptive
tunnel and emits afferent frames on an SSE /feed that pilot's select-dispatcher consumes:

  {kind:"vm_active",    host, ip, slots}     -> a host came active with free capacity (fire now)
  {kind:"vm_recycling", host, status}        -> host draining/deleting (don't fire)
  {kind:"coverage",     covered, total, by_host}   -> shared truth so firers dedup (no double-fire)

8 owns no control: it never fires a test. It watches lambda_ims.vm (status/ip — ip rotates for
is_virtualised) + landings, and recommends. pilot decides; http-mcp acts. Frames are Frame-shaped
(seq, ts, kind, ...) so this folds into the Go collector's hub later.
"""
import os,re,json,time,threading,http.server,socketserver
import pymysql
PORT=int(os.environ.get("IAM_WITNESS_PORT","8788"))
NINE=["10.100.49.250","10.146.192.53","10.252.32.190","10.252.32.191","10.252.32.192","10.252.32.203","10.252.32.205","10.255.32.127","10.255.32.130"]
DESKTOP={"10.146.192.53","10.252.32.190","10.252.32.191","10.252.32.192","10.252.32.203","10.252.32.205"}
ORG=33503825
# the 258 work-set cardinality (for coverage %): android 3x6x9=162 + desktop 4x1x6=24 -> but iam-mine-final desktop=4/version -> keep android 162 here; desktop tracked separately
TOTAL_ANDROID=3*6*9
def db():
    pw=re.search(r"mysql://[^:]+:([^@]+)@",open(os.path.expanduser("~/.lt-tunnels/adaptive-ml-stage.env")).read()).group(1)
    return pymysql.connect(host="127.0.0.1",port=25832,user="rishirajs",password=pw,connect_timeout=10)

STATE={"seq":0,"hosts":{},"covered":set(),"updated":0}
LOCK=threading.Lock()
SUBS=[]   # SSE subscriber queues

def emit(frame):
    with LOCK:
        STATE["seq"]+=1; frame={"seq":STATE["seq"],"ts":round(time.time(),3),**frame}
        line="data: "+json.dumps(frame)+"\n\n"
        dead=[]
        for q in SUBS:
            try: q.append(line)
            except Exception: dead.append(q)
        for q in dead:
            if q in SUBS: SUBS.remove(q)

def watch():
    prev={}
    while True:
        try:
            c=db();cur=c.cursor()
            ph=",".join(["%s"]*len(NINE))
            cur.execute(f"select host_ip,status,ip,allocation_id,org_id from lambda_ims.vm where host_ip in ({ph})",tuple(NINE))
            rows=cur.fetchall()
            # collapse to the active row per host (ip rotates); recycling if no active row
            cur2=c.cursor(); cur2.execute(f"select host_ip,ip,allocation_id,org_id from lambda_ims.vm where host_ip in ({ph}) and status='active'",tuple(NINE))
            active={r[0]:(r[1],r[2],r[3]) for r in cur2.fetchall()}
            # landings (true-ish): active hpsMerge rows with an allocation
            covered_hosts={h for h,(ip,aid,org) in active.items() if aid and str(org)==str(ORG)}
            c.close()
            now={}
            for h in NINE:
                if h in active:
                    ip,aid,org=active[h]; st="busy" if aid else "active"
                    now[h]={"status":st,"ip":ip,"desktop":h in DESKTOP}
                    if prev.get(h,{}).get("status")!=st or prev.get(h,{}).get("ip")!=ip:
                        emit({"kind":"vm_active","host":h,"ip":ip,"slots":0 if aid else 1,"desktop":h in DESKTOP,"busy":bool(aid)})
                else:
                    now[h]={"status":"recycling","ip":None,"desktop":h in DESKTOP}
                    if prev.get(h,{}).get("status")!="recycling":
                        emit({"kind":"vm_recycling","host":h})
            with LOCK:
                STATE["hosts"]=now; STATE["updated"]=round(time.time(),3)
            emit({"kind":"coverage","active":len(active),"of":len(NINE),"active_hosts":sorted(active),"busy_hosts":sorted(covered_hosts)})
            prev=now
        except Exception as e:
            emit({"kind":"error","err":str(e)[:120]})
        time.sleep(5)

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self,*a): pass
    def do_GET(self):
        if self.path.startswith("/feed"):
            self.send_response(200); self.send_header("Content-Type","text/event-stream")
            self.send_header("Cache-Control","no-cache"); self.end_headers()
            q=[];
            with LOCK: SUBS.append(q)
            # replay current state as vm_active/recycling so a late subscriber is caught up
            with LOCK: snap=dict(STATE["hosts"])
            for h,v in snap.items():
                self.wfile.write(("data: "+json.dumps({"kind":"snapshot","host":h,**v})+"\n\n").encode())
            try:
                while True:
                    if q: self.wfile.write(q.pop(0).encode()); self.wfile.flush()
                    else: time.sleep(0.3)
            except Exception:
                with LOCK:
                    if q in SUBS: SUBS.remove(q)
        elif self.path.startswith("/state"):
            with LOCK: body=json.dumps({"seq":STATE["seq"],"updated":STATE["updated"],"hosts":STATE["hosts"]},indent=1)
            self.send_response(200); self.send_header("Content-Type","application/json"); self.end_headers()
            self.wfile.write(body.encode())
        else:
            self.send_response(404); self.end_headers()

if __name__=="__main__":
    threading.Thread(target=watch,daemon=True).start()
    print(f"8 IAM witness on http://127.0.0.1:{PORT}/feed  (SSE)  + /state")
    socketserver.ThreadingTCPServer.allow_reuse_address=True
    with socketserver.ThreadingTCPServer(("127.0.0.1",PORT),H) as s:
        s.serve_forever()
