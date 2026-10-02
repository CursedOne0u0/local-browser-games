#!/usr/bin/env python3
"""Bomb Tag — LAN 2-4 player hot potato (1-2 seats per device, up to 2 devices).
Run:  python3 server.py   (players open the printed LAN URL, same WiFi)
One ticks, all run. Tag to pass. Holder explodes. 2P: rival scores, first to 5. 3-4P: everyone starts at 5, first to 0 loses.
"""
import json, time, math, random, threading, socket, os, sys
from http.server import ThreadingHTTPServer, SimpleHTTPRequestHandler
from pathlib import Path
import urllib.parse
try:
    from qrcodegen import QrCode as _QrCode  # vendored Nayuki encoder (MIT), stdlib-only
except Exception:
    _QrCode = None
def print_qr(url):
    """ASCII QR of the LAN URL for phone scanning. ANSI card on a tty, plain fallback."""
    if _QrCode is None:
        return
    try:
        qr = _QrCode.encode_text(url, _QrCode.Ecc.MEDIUM)
    except Exception:
        return
    n, b = qr.get_size(), 2
    tty = sys.stdout.isatty()
    for y in range(-b, n + b):
        row = ""
        for x in range(-b, n + b):
            dark = qr.get_module(x, y)
            if tty:
                row += "\x1b[40m  \x1b[0m" if dark else "\x1b[47m  \x1b[0m"
            else:
                row += "##" if dark else "  "
        print("  " + row)

PORT = int(os.environ.get("PORT", "3002"))
VERSION = "1.28"  # bump on every update; shown on the site
WIN_ROUNDS = 5  # 2P: points to win. 3-4P: total blasts per match (highest score wins).
MAX_SEATS = 4
W, H = 800, 560  # base arena (2P); 3-4P scales to drift size below
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
# pillars (x,y,w,h) to juke around — authored for 800x560, scaled to arena
PILLARS_BASE = [
    {"x": 200, "y": 140, "w": 60, "h": 60},
    {"x": 540, "y": 140, "w": 60, "h": 60},
    {"x": 200, "y": 360, "w": 60, "h": 60},
    {"x": 540, "y": 360, "w": 60, "h": 60},
    {"x": 370, "y": 250, "w": 60, "h": 60},
]
SPAWNS_BASE = [{"x": 100, "y": 280}, {"x": 700, "y": 280},
               {"x": 100, "y": 100}, {"x": 700, "y": 460}]
def arena_for(n):
    # 3rd player grows the room to drift size (960x600); 2P stays classic
    return (960, 600) if n >= 3 else (800, 560)
def layout_for(aw, ah):
    sx, sy = aw/800, ah/560
    pillars = [{"x": round(p["x"]*sx), "y": round(p["y"]*sy),
                "w": max(40, round(p["w"]*sx)), "h": max(40, round(p["h"]*sy))} for p in PILLARS_BASE]
    spawns = [{"x": round(min(max(60, s["x"]*sx), aw-60)), "y": round(min(max(60, s["y"]*sy), ah-60))} for s in SPAWNS_BASE]
    return pillars, spawns
# boost pads spawn at random spots aiming random ways; one-shot slingshots
PAD_R = 30
BOOST_TIME = 0.55
BOOST_MULT = 1.7
MAX_PADS = 2
PAD_LIFE = 10.0

lock = threading.Lock()
game = {
    "phase": "waiting",  # waiting|countdown|playing|round|over
    "countdown_end": 0,
    "round_end": 0,
    "round": 1,
    "players": [None, None, None, None],  # {id,name,last_seen,ready,x,y,input,fire,prev_fire}
    "pos": [{"x": 0, "y": 0, "score": 0} for _ in range(4)],
    "dash_until": [0, 0, 0, 0],
    "dash_cd": [0, 0, 0, 0],
    "imm_until": [0, 0, 0, 0],
    "holder": 0,
    "fuse": 10.0,
    "pads": [],  # live pads {x,y,dx,dy,expires}
    "pad_timer": 2.5,
    "boost_until": [0, 0, 0, 0],
    "last_boost": None,  # {x,y,by,id}
    "winner": 0,
    "loser": 0,  # most recent elimination (3-4P)
    "out": [False]*4,  # eliminated seats stay down until next match
    "event_id": 0,
    "last_pass": None,  # {holder,id}
    "last_boom": None,  # {x,y,scorer,id}
    "paused": False, "paused_by": "", "paused_since": 0,
    "aw": 800, "ah": 560,  # live arena dims (grow at 3P)
    "pillars": [], "spawns": [],  # layout_for() output for aw/ah
    "nstart": 2,  # occupied seats when the current match began
    "blasts": 0,  # explosions this match (3-4P match length)
}

def now(): return time.time()

def shift_paused(d):
    # thaw every absolute deadline by the paused duration (presence timers untouched)
    game["countdown_end"] += d; game["round_end"] += d
    for k in ("dash_until", "dash_cd", "imm_until", "boost_until"):
        game[k] = [t+d if t else t for t in game[k]]
    for pd in game["pads"]:
        pd["expires"] += d

def reset_positions():
    for i, s in enumerate(game["spawns"] or SPAWNS_BASE):
        game["pos"][i]["x"] = s["x"]; game["pos"][i]["y"] = s["y"]
    n = len(game["pos"])
    game["imm_until"] = [0]*n
    game["dash_until"] = [0]*n; game["dash_cd"] = [0]*n
    game["pads"] = []; game["pad_timer"] = 2.5; game["boost_until"] = [0]*n

def spawn_pad():
    if len(game["pads"]) >= MAX_PADS: return
    aw, ah, pillars = game["aw"], game["ah"], game["pillars"]
    for _ in range(30):
        x = 70+random.random()*(aw-140); y = 70+random.random()*(ah-140)
        if any(pl["x"]-25 < x < pl["x"]+pl["w"]+25 and pl["y"]-25 < y < pl["y"]+pl["h"]+25 for pl in pillars):
            continue
        if all(math.hypot(x-t["x"], y-t["y"]) > 120 for t in game["pos"]):
            a = random.random()*2*math.pi
            game["pads"].append({"x": x, "y": y, "dx": math.cos(a), "dy": math.sin(a),
                                 "expires": now()+PAD_LIFE})
            return

def reset_scores():
    # 2P duel starts at 0 (first to WIN_ROUNDS); 3-4P starts at 5, first to 0 loses
    start = 5 if len(occupied()) > 2 else 0
    for p in game["pos"]: p["score"] = start
    game["winner"] = 0; game["loser"] = 0; game["round"] = 1; game["blasts"] = 0
    game["out"] = [False]*len(game["pos"])

def occupied():
    return [i for i, p in enumerate(game["players"]) if p]
def alive():
    return [i for i in occupied() if not game["out"][i]]

def apply_arena(n):
    game["aw"], game["ah"] = arena_for(n)
    game["pillars"], game["spawns"] = layout_for(game["aw"], game["ah"])

def new_round():
    reset_positions()
    occ = occupied() or [0, 1]
    pool = [i+1 for i in occ if not game["out"][i]] or [i+1 for i in occ]
    if game["holder"] not in pool:
        game["holder"] = random.choice(pool)
    else:
        others = [h for h in pool if h != game["holder"]] or [game["holder"]]
        game["holder"] = random.choice(others)
    game["fuse"] = 8+random.random()*6
    # NOTE: no last_pass event here — assignment is shown via holder/names, not the pass toast.
    # Clear any previous round's pass so a client polling late never toasts it as new at round start.
    game["last_pass"] = None

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
    # arena bounds (live dims) + pillars (live layout)
    aw, ah, pillars = game["aw"], game["ah"], game["pillars"]
    x = max(RUN_R, min(aw-RUN_R, x)); y = max(RUN_R, min(ah-RUN_R, y))
    for pl in pillars:
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
    occ = occupied()
    game["event_id"] += 1
    game["last_boom"] = {"x": round(game["pos"][h]["x"],1), "y": round(game["pos"][h]["y"],1),
                         "scorer": 0, "id": game["event_id"]}
    game["last_pass"] = None
    if game["nstart"] <= 2:
        # classic duel: nearest rival scores, first to WIN_ROUNDS
        # (== the rival in pure 2P; robust if a 3rd seat joins mid-match)
        hx, hy = game["pos"][h]["x"], game["pos"][h]["y"]
        scorer, bd = 1, None
        for i in occ:
            if i == h: continue
            d = math.hypot(hx-game["pos"][i]["x"], hy-game["pos"][i]["y"])
            if bd is None or d < bd: scorer, bd = i+1, d
        game["pos"][scorer-1]["score"] += 1
        game["last_boom"]["scorer"] = scorer
        if game["pos"][scorer-1]["score"] >= WIN_ROUNDS:
            game["phase"] = "over"; game["winner"] = scorer
        else:
            game["phase"] = "round"; game["round_end"] = now()+2.5
    else:
        # elimination: the holder loses a point; at 0 they're out, last one standing wins
        game["pos"][h]["score"] -= 1
        game["last_boom"]["holder"] = h+1
        game["blasts"] += 1
        if game["pos"][h]["score"] <= 0:
            game["pos"][h]["score"] = 0
            game["out"][h] = True
            game["loser"] = h+1
            rest = [i+1 for i in alive()]
            if len(rest) == 1:
                game["phase"] = "over"; game["winner"] = rest[0]
            elif not rest:
                game["phase"] = "over"; game["winner"] = 0
            else:
                game["phase"] = "round"; game["round_end"] = now()+2.5
        else:
            game["phase"] = "round"; game["round_end"] = now()+2.5

def step(dt):
    if game["phase"] == "countdown" and now() >= game["countdown_end"]:
        game["phase"] = "playing"
        return
    if game["phase"] == "round" and now() >= game["round_end"]:
        game["round"] += 1
        apply_arena(len(occupied()))  # late joiners grow the room at the round break
        new_round()
        game["phase"] = "playing"
        return
    if game["phase"] != "playing": return
    t = now()
    occ = occupied()
    # holder seat freed mid-round (ragequit): hand the bomb to a random live seat.
    # Nobody connected: let the sim run on (fuse burns, match resolves itself).
    if occ and game["holder"]-1 not in occ:
        pool = [i for i in occ if not game["out"][i]] or occ
        game["holder"] = random.choice(pool)+1
    for i in range(MAX_SEATS):
        if game["out"][i]: continue  # eliminated runners sit the rest out, frozen
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
        if t < game["boost_until"][i]: spd *= BOOST_MULT
        x, y = game["pos"][i]["x"]+ix*spd*dt, game["pos"][i]["y"]+iy*spd*dt
        game["pos"][i]["x"], game["pos"][i]["y"] = collide(x, y)
        # boost pads: random spawns, one-shot slingshots along their arrow
        game["pad_timer"] -= dt
        if game["pad_timer"] <= 0:
            spawn_pad()
            game["pad_timer"] = 4+random.random()*3
        for pd in game["pads"]:
            if now() >= pd["expires"]:
                pd["dead"] = True
                continue
            for i in range(MAX_SEATS):
                if not game["players"][i]: continue
                if math.hypot(game["pos"][i]["x"]-pd["x"], game["pos"][i]["y"]-pd["y"]) < PAD_R:
                    pd["dead"] = True
                    game["boost_until"][i] = t+BOOST_TIME
                    game["pos"][i]["x"], game["pos"][i]["y"] = collide(
                        game["pos"][i]["x"]+pd["dx"]*95, game["pos"][i]["y"]+pd["dy"]*95)
                    game["event_id"] += 1
                    game["last_boost"] = {"x": pd["x"], "y": pd["y"], "by": i+1, "id": game["event_id"]}
                    break
        game["pads"] = [pd for pd in game["pads"] if not pd.get("dead")]
    # tag pass: holder tags the nearest other live runner in reach
    h = game["holder"]-1
    best, bd = -1, TAG_DIST
    for i in occ:
        if i == h or game["out"][i]: continue
        d = math.hypot(game["pos"][h]["x"]-game["pos"][i]["x"],
                       game["pos"][h]["y"]-game["pos"][i]["y"])
        if d < bd: best, bd = i, d
    if best >= 0 and t >= game["imm_until"][h]:
        o = best
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
            if game["paused"]:
                pass  # frozen for all: no sim, no transitions, no deadlines
            else:
                occ = occupied()
                if game["phase"] == "waiting" and len(occ) >= 2 and all(game["players"][i]["ready"] for i in occ):
                    game["phase"] = "countdown"; game["countdown_end"] = now()+2.4
                    game["nstart"] = len(occ)
                    apply_arena(len(occ))
                    reset_scores(); new_round()
                    game["last_pass"] = None; game["last_boom"] = None  # no stale toasts
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
        "aw": game["aw"], "ah": game["ah"],
        "pillars": game["pillars"],
        "runners": [{"x": round(game["pos"][i]["x"],1), "y": round(game["pos"][i]["y"],1),
                     "score": game["pos"][i]["score"],
                     "out": game["out"][i],
                     "dash": round(max(0, game["dash_until"][i]-t),2),
                     "dash_cd": round(max(0, game["dash_cd"][i]-t),2),
                     "boost": round(max(0, game["boost_until"][i]-t),2),
                     "imm": round(max(0, game["imm_until"][i]-t),2)} for i in range(MAX_SEATS)],
        "pads": [{"x": round(pd["x"],1), "y": round(pd["y"],1),
                  "dx": round(pd["dx"],3), "dy": round(pd["dy"],3),
                  "ttl": round(max(0, pd["expires"]-t),2)} for pd in game["pads"]],
        "holder": game["holder"] if game["phase"] in ("playing", "round") else 0,
        "fuse": round(max(0, game["fuse"]),2),
        "winner": game["winner"],
        "loser": game["loser"],
        "blasts": game["blasts"],
        "names": [((game["players"][i] or {}).get("name","") or "") for i in range(MAX_SEATS)],
        "ready": [bool(game["players"][i] and game["players"][i]["ready"]) for i in range(MAX_SEATS)],
        "connected": [bool(game["players"][i]) for i in range(MAX_SEATS)],
        "last_pass": game["last_pass"],
        "last_boom": game["last_boom"],
        "last_boost": game["last_boost"],
        "paused": game["paused"],
        "paused_by": game["paused_by"],
        "event_id": game["event_id"],
    }

class Handler(SimpleHTTPRequestHandler):
    protocol_version = "HTTP/1.1"  # keep-alive: no TCP+TLS handshake per 60Hz poll
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
            self.send_header("Cache-Control","no-store"); self.send_header("Content-Length",str(len(b))); self.end_headers(); self.wfile.write(b)
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
                        s=next((i for i in range(MAX_SEATS) if not game["players"][i]),-1)
                        if s<0: return self._json({"you":0})
                        game["players"][s]={"id":pid,"name":name,"last_seen":now(),"ready":False,
                                            "input":{"x":0,"y":0},"fire":False,"prev_fire":False}
                        game["out"][s] = False  # fresh seat: back in (rejoining an out seat rejoins)
                        # mid-match join: drop in at a free spawn;
                        # duel: current min; elimination: current max (fresh legs, not punished)
                        if game["phase"] in ("countdown","playing","round"):
                            occ=[i for i in occupied() if i!=s]
                            scores=[game["pos"][i]["score"] for i in occ] or [0]
                            floor=max(scores) if len(occ)+1 >= 3 else min(scores)
                            sp=(game["spawns"] or [{"x":100,"y":280}])[s%len(game["spawns"] or [1])]
                            game["pos"][s]={"x":sp["x"],"y":sp["y"],"score":floor}
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
                if self.path=="/api/leave":
                    if s>=0:  # browser closed: free the seat now, don't wait out the 8s timeout
                        game["players"][s]=None
                    return self._json({"ok":True})
                if self.path=="/api/pause":
                    # pause-vote: any player freezes the sim for all; any resume thaws deadlines
                    if game["paused"]:
                        shift_paused(now()-game["paused_since"])
                        game["paused"]=False; game["paused_by"]=""
                    else:
                        p = game["players"][s] if s>=0 else None
                        game["paused"]=True
                        game["paused_by"]=(p["name"] if p and p.get("name") else "Someone")
                        game["paused_since"]=now()
                    return self._json({"ok":True,"paused":game["paused"],"by":game["paused_by"]})
                if self.path=="/api/restart":
                    if game["phase"]=="over":
                        reset_scores(); new_round()
                        game["last_pass"] = None; game["last_boom"] = None
                        for p in game["players"]:
                            if p: p["ready"]=False
                        game["phase"]="waiting"; game["winner"]=0; game["loser"]=0; game["round"]=1
                    return self._json({"ok":True})
            return self._json({"ok":False},400)
        self.send_response(404); self.send_header("Content-Length","0"); self.end_headers()

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
    srv.socket.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)  # no Nagle delay on small packets
    print(f"\n  BOMB TAG running!\n  On this laptop:  http://localhost:{PORT}")
    _ips = lan_ips()
    for ip in _ips: print(f"  Friend on same WiFi:  http://{ip}:{PORT}")
    if _ips:
        print("  Scan to join (same WiFi):")
        print_qr(f"http://{_ips[0]}:{PORT}")
    print(f"\n  WASD/arrows to run, SPACE to dash. Holder is slower — pass it!\n  2P: rival scores, first to {WIN_ROUNDS}. 3-4P: everyone starts at 5, first to 0 loses. Room grows at 3P!\n")
    try: srv.serve_forever()
    except KeyboardInterrupt: print("\nbye!")
