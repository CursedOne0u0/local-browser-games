#!/usr/bin/env python3
"""Bomb Tag — LAN 2-player hot potato. Zero dependencies, stdlib only.
Run:  python3 server.py   (both players open the printed LAN URL, same WiFi)
One ticks, both run. Tag to pass. Holder explodes. First to 5.
"""
import json, time, math, random, threading, socket, os
from http.server import ThreadingHTTPServer, SimpleHTTPRequestHandler
from pathlib import Path
import urllib.parse

PORT = int(os.environ.get("PORT", "3002"))
VERSION = "1.0"  # bump on every update; shown on the site
WIN_ROUNDS = 5
PUBLIC = Path(__file__).parent / "public"

W, H = 800, 560
RUN_R = 15
RUN_SPEED = 235
HOLDER_SPEED = 210
DASH_MULT = 2.3
DASH_TIME = 0.28
DASH_CD = 3.0
TAG_DIST = 36
TAG_IMMUNITY = 1.0
# pillars (x,y,w,h) to juke around
PILLARS = [
    {"x": 200, "y": 140, "w": 60, "h": 60},
    {"x": 540, "y": 140, "w": 60, "h": 60},
    {"x": 200, "y": 360, "w": 60, "h": 60},
    {"x": 540, "y": 360, "w": 60, "h": 60},
    {"x": 370, "y": 250, "w": 60, "h": 60},
]
SPAWNS = [{"x": 100, "y": 280}, {"x": 700, "y": 280}]

lock = threading.Lock()
game = {
    "phase": "waiting",  # waiting|countdown|playing|round|over
    "countdown_end": 0,
    "round_end": 0,
    "round": 1,
    "players": [None, None],  # {id,name,last_seen,ready,x,y,input,fire,prev_fire}
    "pos": [{"x": 0, "y": 0, "score": 0}, {"x": 0, "y": 0, "score": 0}],
    "dash_until": [0, 0],
    "dash_cd": [0, 0],
    "imm_until": [0, 0],
    "holder": 1,
    "fuse": 10.0,
    "winner": 0,
    "event_id": 0,
    "last_pass": None,  # {holder,id}
    "last_boom": None,  # {x,y,scorer,id}
}

def now(): return time.time()

def reset_positions():
    for i, s in enumerate(SPAWNS):
        game["pos"][i]["x"] = s["x"]; game["pos"][i]["y"] = s["y"]
    game["imm_until"] = [0, 0]
    game["dash_until"] = [0, 0]; game["dash_cd"] = [0, 0]

def reset_scores():
    for p in game["pos"]: p["score"] = 0
    game["winner"] = 0; game["round"] = 1

def new_round():
    reset_positions()
    game["holder"] = random.choice([1, 2])
    game["fuse"] = 8+random.random()*6
    game["event_id"] += 1
    game["last_pass"] = {"holder": game["holder"], "id": game["event_id"]}

def touch(pid):
    for p in game["players"]:
        if p and p["id"] == pid: p["last_seen"] = now()
def slot_of(pid):
    for i, p in enumerate(game["players"]):
        if p and p["id"] == pid: return i
    return -1
def free_stale():
    for i, p in enumerate(game["players"]):
        if p and now() - p["last_seen"] > 8:
            game["players"][i] = None

def collide(x, y):
    # arena bounds
    x = max(RUN_R, min(W-RUN_R, x)); y = max(RUN_R, min(H-RUN_R, y))
    # pillars: push out
    for pl in PILLARS:
        cx = max(pl["x"], min(pl["x"]+pl["w"], x))
        cy = max(pl["y"], min(pl["y"]+pl["h"], y))
        dx, dy = x-cx, y-cy
        d2 = dx*dx+dy*dy
        if d2 < RUN_R*RUN_R:
            if d2 > 1e-6:
                d = math.sqrt(d2)
                x = cx+dx/d*RUN_R; y = cy+dy/d*RUN_R
            else:  # center inside: push along min axis
                l, r, t, b = x-pl["x"], pl["x"]+pl["w"]-x, y-pl["y"], pl["y"]+pl["h"]-y
                m = min(l, r, t, b)
                if m == l: x = pl["x"]-RUN_R
                elif m == r: x = pl["x"]+pl["w"]+RUN_R
                elif m == t: y = pl["y"]-RUN_R
                else: y = pl["y"]+pl["h"]+RUN_R
    return x, y

def explode():
    h = game["holder"]-1
    scorer = 2-h  # 1->2, 2->1
    game["pos"][scorer-1]["score"] += 1
    game["event_id"] += 1
    game["last_boom"] = {"x": round(game["pos"][h]["x"],1), "y": round(game["pos"][h]["y"],1),
                         "scorer": scorer, "id": game["event_id"]}
    if game["pos"][scorer-1]["score"] >= WIN_ROUNDS:
        game["phase"] = "over"; game["winner"] = scorer
    else:
        game["phase"] = "round"; game["round_end"] = now()+2.5

def step(dt):
    if game["phase"] == "countdown" and now() >= game["countdown_end"]:
        game["phase"] = "playing"
        return
    if game["phase"] == "round" and now() >= game["round_end"]:
        game["round"] += 1
        new_round()
        game["phase"] = "playing"
        return
    if game["phase"] != "playing": return
    t = now()
    for i in (0, 1):
        p = game["players"][i]
        ix = iy = 0
        if p:
            ix = max(-1, min(1, p["input"]["x"])); iy = max(-1, min(1, p["input"]["y"]))
            if p["fire"] and not p["prev_fire"] and t >= game["dash_cd"][i]:
                game["dash_until"][i] = t+DASH_TIME; game["dash_cd"][i] = t+DASH_CD
            p["prev_fire"] = p["fire"]
        n = math.hypot(ix, iy)
        if n > 1: ix/=n; iy/=n
        spd = (HOLDER_SPEED if game["holder"] == i+1 else RUN_SPEED)
        if t < game["dash_until"][i]: spd *= DASH_MULT
        x, y = game["pos"][i]["x"]+ix*spd*dt, game["pos"][i]["y"]+iy*spd*dt
        game["pos"][i]["x"], game["pos"][i]["y"] = collide(x, y)
    # tag pass
    h = game["holder"]-1
    o = 1-h
    d = math.hypot(game["pos"][h]["x"]-game["pos"][o]["x"],
                   game["pos"][h]["y"]-game["pos"][o]["y"])
    if d < TAG_DIST and t >= game["imm_until"][h]:
        game["holder"] = o+1
        game["imm_until"][o] = t+TAG_IMMUNITY
        # shove apart so it can't instantly bounce back
        dx = game["pos"][o]["x"]-game["pos"][h]["x"]
        dy = game["pos"][o]["y"]-game["pos"][h]["y"]
        dd = math.hypot(dx, dy) or 1
        game["pos"][h]["x"], game["pos"][h]["y"] = collide(
            game["pos"][h]["x"]-dx/dd*24, game["pos"][h]["y"]-dy/dd*24)
        game["event_id"] += 1
        game["last_pass"] = {"holder": o+1, "id": game["event_id"]}
    # fuse
    game["fuse"] -= dt
    if game["fuse"] <= 0:
        game["fuse"] = 0
        explode()

def game_loop():
    last = time.time()
    while True:
        t = time.time(); dt = min(0.05, t-last); last = t
        with lock:
            free_stale()
            p0, p1 = game["players"]
            if game["phase"] == "waiting" and p0 and p1 and p0["ready"] and p1["ready"]:
                game["phase"] = "countdown"; game["countdown_end"] = now()+2.4
                reset_scores(); new_round()
            step(dt)
        time.sleep(1/60)

def snapshot():
    cd = 0
    if game["phase"] == "countdown": cd = max(1, math.ceil(game["countdown_end"]-now()))
    t = now()
    return {
        "v": VERSION,
        "phase": game["phase"],
        "countdown": cd,
        "round": game["round"],
        "pillars": PILLARS,
        "runners": [{"x": round(game["pos"][i]["x"],1), "y": round(game["pos"][i]["y"],1),
                     "score": game["pos"][i]["score"],
                     "dash": round(max(0, game["dash_until"][i]-t),2),
                     "dash_cd": round(max(0, game["dash_cd"][i]-t),2),
                     "imm": round(max(0, game["imm_until"][i]-t),2)} for i in (0, 1)],
        "holder": game["holder"] if game["phase"] in ("playing", "round") else 0,
        "fuse": round(max(0, game["fuse"]),2),
        "winner": game["winner"],
        "names": [(game["players"][0] or {}).get("name","") or "", (game["players"][1] or {}).get("name","") or ""],
        "ready": [bool(game["players"][0] and game["players"][0]["ready"]), bool(game["players"][1] and game["players"][1]["ready"])],
        "connected": [bool(game["players"][0]), bool(game["players"][1])],
        "last_pass": game["last_pass"],
        "last_boom": game["last_boom"],
        "event_id": game["event_id"],
    }

class Handler(SimpleHTTPRequestHandler):
    def __init__(self,*a,**kw): super().__init__(*a, directory=str(PUBLIC), **kw)
    def log_message(self,*a): pass
    def handle_one_request(self):
        try:
            super().handle_one_request()
        except (ConnectionResetError, BrokenPipeError):
            try: self.connection.close()
            except Exception: pass
    def _json(self,obj,code=200):
        b=json.dumps(obj).encode()
        try:
            self.send_response(code); self.send_header("Content-Type","application/json")
            self.send_header("Content-Length",str(len(b))); self.end_headers(); self.wfile.write(b)
        except (ConnectionResetError, BrokenPipeError):
            pass
    def _body(self):
        try: n=int(self.headers.get("Content-Length",0))
        except: n=0
        return json.loads(self.rfile.read(n).decode() or "{}") if n else {}
    def do_GET(self):
        parsed=urllib.parse.urlparse(self.path)
        if parsed.path=="/api/state":
            qs=urllib.parse.parse_qs(parsed.query)
            pid=(qs.get("id") or [""])[0]
            with lock:
                touch(pid)
                s=snapshot()
            return self._json(s)
        return super().do_GET()
    def do_POST(self):
        if self.path.startswith("/api/"):
            d=self._body()
            pid=str(d.get("clientId",""))[:32]
            with lock:
                if self.path=="/api/join":
                    name=str(d.get("name","Player"))[:32] or "Player"
                    s=slot_of(pid)
                    if s<0:
                        free_stale()
                        if not game["players"][0]: s=0
                        elif not game["players"][1]: s=1
                        else: return self._json({"you":0})
                        game["players"][s]={"id":pid,"name":name,"last_seen":now(),"ready":False,
                                            "input":{"x":0,"y":0},"fire":False,"prev_fire":False}
                    else:
                        game["players"][s]["name"]=name; game["players"][s]["last_seen"]=now()
                    return self._json({"you":s+1})
                touch(pid); s=slot_of(pid)
                if self.path=="/api/input" and s>=0:
                    try:
                        ix=max(-1,min(1,float(d.get("x",0)))); iy=max(-1,min(1,float(d.get("y",0))))
                    except: ix,iy=0,0
                    game["players"][s]["input"]={"x":ix,"y":iy}
                    game["players"][s]["fire"]=bool(d.get("fire",False))
                    return self._json({"ok":True})
                if self.path=="/api/ready" and s>=0:
                    game["players"][s]["ready"]=True
                    return self._json({"ok":True})
                if self.path=="/api/restart":
                    if game["phase"]=="over":
                        reset_scores(); new_round()
                        for p in game["players"]:
                            if p: p["ready"]=False
                        game["phase"]="waiting"; game["winner"]=0; game["round"]=1
                    return self._json({"ok":True})
            return self._json({"ok":False},400)
        self.send_response(404); self.end_headers()

def lan_ips():
    out=[]
    try:
        s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.connect(("8.8.8.8",80))
        out.append(s.getsockname()[0]); s.close()
    except: pass
    try:
        for _,_,_,_,addr in socket.getaddrinfo(socket.gethostname(),None):
            ip=addr[0]
            if "." in ip and not ip.startswith("127."): out.append(ip)
    except: pass
    return list(dict.fromkeys(out))

if __name__=="__main__":
    PUBLIC.mkdir(exist_ok=True)
    reset_positions()
    threading.Thread(target=game_loop,daemon=True).start()
    srv=ThreadingHTTPServer(("0.0.0.0",PORT),Handler)
    print(f"\n  BOMB TAG running!\n  On this laptop:  http://localhost:{PORT}")
    for ip in lan_ips(): print(f"  Friend on same WiFi:  http://{ip}:{PORT}")
    print(f"\n  WASD/arrows to run, SPACE to dash. Holder is slower — pass it!\n  First to {WIN_ROUNDS} blasts wins!\n")
    try: srv.serve_forever()
    except KeyboardInterrupt: print("\nbye!")
