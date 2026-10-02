#!/usr/bin/env python3
"""Bastion Bros LAN — 2-4 player co-op tower defense server. Zero deps, stdlib only.
Run:  python3 server.py   (each defender opens the printed LAN URL on their own screen)
Authoritative sim: grid, gold, base, waves, enemies. Clients send move/build intents.
Build anytime (mid-wave +25%). Repair by building on your own tower (75%).
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

PORT = int(os.environ.get("PORT", "3005"))
VERSION = "1.0"  # bump on every update; shown on the site
MAX_SEATS = 4
MAXBASE = 20
W, H, COLS, ROWS, CELL = 960, 600, 16, 10, 60
TOWERS = {
    "arrow": {"cost": 36, "dmg": 8, "rate": 1.5, "range": 150, "hp": 100},
    "cannon": {"cost": 85, "dmg": 22, "rate": 0.7, "range": 170, "splash": 55, "hp": 160},
    "frost": {"cost": 65, "dmg": 3, "rate": 1.2, "range": 150, "slow": 0.55, "slowT": 2, "hp": 120},
}
TNAMES = ["arrow", "cannon", "frost"]
PATH = [(c, 1) for c in range(15)] + [(14, 2), (14, 3), (14, 4)] + \
       [(c, 4) for c in range(13, 0, -1)] + [(1, 5), (1, 6), (1, 7)] + \
       [(c, 7) for c in range(2, 16)]
PATHSET = set(PATH)
WAYPTS = [{"x": c*CELL+CELL/2, "y": r*CELL+CELL/2} for c, r in PATH]
SEGLENS = [math.hypot(WAYPTS[i+1]["x"]-WAYPTS[i]["x"], WAYPTS[i+1]["y"]-WAYPTS[i]["y"])
           for i in range(len(WAYPTS)-1)]
EKIND = {
    "walker": {"hp": 20, "spd": 55, "bounty": 4, "leak": 1},
    "runner": {"hp": 12, "spd": 95, "bounty": 5, "leak": 1},
    "brute": {"hp": 70, "spd": 40, "bounty": 10, "leak": 3},
    "lord": {"hp": 500, "spd": 44, "bounty": 70, "leak": 6},
}
PUBLIC = Path(__file__).parent / "public"

lock = threading.Lock()
game = {
    "phase": "waiting",  # waiting|countdown|build|combat|over
    "countdown_end": 0,
    "level": 0, "wave": 0,
    "players": [None]*MAX_SEATS,
    "grid": [],  # {c,r,type,hp,cd}
    "gold": 0, "base": MAXBASE,
    "enemies": [],  # {kind,x,y,hp,maxhp,seg,segT,segLen,slowT,smashT}
    "spawnQueue": [], "spawnT": 0,
    "winner": 0, "event_id": 0,
    "last_wave": None, "last_over": None, "last_build": None,
    "paused": False, "paused_by": "", "paused_since": 0,
}

def now(): return time.time()
def waves_for(lv): return lv+2
def enemy_hp(kind):
    m = 1+0.28*(game["level"]-1)
    return round({"walker": 20, "runner": 12, "brute": 70, "lord": 500}[kind]*m)
def enemy_spd(kind):
    m = 1+0.07*(game["level"]-1)
    return {"walker": 55, "runner": 95, "brute": 40, "lord": 44}[kind]*m

def occupied():
    return [i for i, p in enumerate(game["players"]) if p]
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

def tile_free(c, r):
    if not (0 <= c < COLS and 0 <= r < ROWS): return False
    if (c, r) in PATHSET: return False
    return not any(t["c"] == c and t["r"] == r for t in game["grid"])

def tower_cost(ttype):
    base = TOWERS[ttype]["cost"]
    return math.ceil(base*1.25) if game["phase"] == "combat" else base

def try_build(i):
    p = game["players"][i]
    c, r = int(p["x"]//CELL), int(p["y"]//CELL)
    ttype = TNAMES[p["sel"] % 3]
    t = next((t for t in game["grid"] if t["c"] == c and t["r"] == r), None)
    if t:
        mx = TOWERS[t["type"]]["hp"]
        if t["hp"] >= mx: return {"ok": False, "why": "full"}
        cost = math.ceil(TOWERS[t["type"]]["cost"]*0.75)
        if game["gold"] < cost: return {"ok": False, "why": "poor"}
        game["gold"] -= cost; t["hp"] = mx
        return {"ok": True, "what": "repair"}
    if not tile_free(c, r): return {"ok": False, "why": "blocked"}
    cost = tower_cost(ttype)
    if game["gold"] < cost: return {"ok": False, "why": "poor"}
    game["gold"] -= cost
    game["grid"].append({"c": c, "r": r, "type": ttype, "hp": TOWERS[ttype]["hp"], "cd": 0,
                         "x": c*CELL+CELL/2, "y": r*CELL+CELL/2})
    return {"ok": True, "what": "build", "type": ttype}

def next_level():
    game["level"] += 1
    game["wave"] = 0
    game["phase"] = "build"
    game["event_id"] += 1
    game["last_wave"] = {"level": game["level"], "wave": 0, "id": game["event_id"]}

def build_wave():
    lv, wv = game["level"], game["wave"]+1
    q = []
    n = 7+wv*3+lv*2
    if lv >= 3: unlock = ["walker"]*2+["runner"]*2+["brute"]
    elif lv >= 2: unlock = ["walker"]*3+["runner"]+["brute"]
    else: unlock = ["walker"]*3+["runner"]
    for _ in range(n):
        q.append(random.choice(unlock))
    if wv % 3 == 0: q.append("lord")
    random.shuffle(q)
    return q

def start_wave():
    if game["phase"] != "build": return False
    game["wave"] += 1
    game["spawnQueue"] = build_wave()
    game["spawnT"] = 0
    game["phase"] = "combat"
    game["event_id"] += 1
    game["last_wave"] = {"level": game["level"], "wave": game["wave"], "id": game["event_id"]}
    return True

def spawn_interval():
    return max(0.35, 2.0-0.18*(game["wave"]+game["level"]))
def spawn_enemy(kind):
    lv = game["level"]
    spd = {"walker": 55, "runner": 95, "brute": 40, "lord": 44}[kind]*(1+0.04*(lv-1))
    hp = enemy_hp(kind)
    game["enemies"].append({"kind": kind, "seg": 0, "segT": 0, "segLen": SEGLENS[0],
                            "slowT": 0, "smashT": 0, "spd": spd,
                            "hp": hp, "maxhp": hp,
                            "x": WAYPTS[0]["x"], "y": WAYPTS[0]["y"]})

def hurt_foe(e, dmg):
    e["hp"] -= dmg
    if e["hp"] <= 0 and not e.get("dead"):
        e["dead"] = True
        game["gold"] += {"walker": 4, "runner": 5, "brute": 10, "lord": 70}[e["kind"]]

def game_over():
    game["phase"] = "over"
    game["event_id"] += 1
    game["last_over"] = {"level": game["level"], "id": game["event_id"]}

def shift_paused(d):
    game["countdown_end"] += d

def step(dt):
    if game["phase"] == "countdown" and now() >= game["countdown_end"]:
        game["phase"] = "build"
        return
    # avatars (build cursors)
    for i in occupied():
        p = game["players"][i]
        ix = max(-1, min(1, p["input"]["x"])); iy = max(-1, min(1, p["input"]["y"]))
        n = math.hypot(ix, iy)
        if n > 1: ix /= n; iy /= n
        if n > 0.05:
            p["x"] = max(20, min(W-20, p["x"]+ix*340*dt))
            p["y"] = max(20, min(H-20, p["y"]+iy*340*dt))
        if p["build"]:
            p["build"] = False
            r = try_build(i)
            if r["ok"]:
                game["event_id"] += 1
                game["last_build"] = {"seat": i+1, "what": r.get("what", ""),
                                      "type": r.get("type", ""), "id": game["event_id"]}
    if game["phase"] != "combat": return
    if game["spawnQueue"]:
        game["spawnT"] -= dt
        if game["spawnT"] <= 0:
            game["spawnT"] = spawn_interval()
            spawn_enemy(game["spawnQueue"].pop())
    # towers fire
    for t in game["grid"]:
        t["cd"] -= dt
        T = TOWERS[t["type"]]
        best, bd = None, T["range"]*T["range"]
        for e in game["enemies"]:
            if e.get("dead"): continue
            d = (e["x"]-t["x"])**2+(e["y"]-t["y"])**2
            if d < bd: bd, best = d, e
        if best is not None and t["cd"] <= 0:
            t["cd"] = 1/T["rate"]
            if t["type"] == "cannon":
                for e in game["enemies"]:
                    if not e.get("dead") and math.hypot(e["x"]-best["x"], e["y"]-best["y"]) < T["splash"]:
                        hurt_foe(e, T["dmg"])
            elif t["type"] == "frost":
                hurt_foe(best, T["dmg"]); best["slowT"] = T["slowT"]
            else:
                hurt_foe(best, T["dmg"])
    # enemies walk
    for e in game["enemies"]:
        if e.get("dead"): continue
        if e["slowT"] > 0: e["slowT"] -= dt
        e["segT"] += e["spd"]*(0.55 if e["slowT"] > 0 else 1)*dt
        while e["segT"] >= e["segLen"]:
            e["segT"] -= e["segLen"]; e["seg"] += 1
            if e["seg"] >= len(SEGLENS):
                leak = {"walker": 1, "runner": 1, "brute": 3, "lord": 6}[e["kind"]]
                game["base"] -= leak
                e["dead"] = True
                if game["base"] <= 0:
                    game["base"] = 0; game_over(); return
                break
            e["segLen"] = SEGLENS[e["seg"]]
        if e.get("dead"): continue
        a, b = WAYPTS[e["seg"]], WAYPTS[e["seg"]+1]
        f = e["segT"]/e["segLen"] if e["segLen"] else 0
        e["x"], e["y"] = a["x"]+(b["x"]-a["x"])*f, a["y"]+(b["y"]-a["y"])*f
        if e["kind"] in ("brute", "lord"):
            e["smashT"] -= dt
            if e["smashT"] <= 0:
                e["smashT"] = 1
                for t in game["grid"]:
                    if math.hypot(t["x"]-e["x"], t["y"]-e["y"]) < 50:
                        t["hp"] -= 10
    game["grid"] = [t for t in game["grid"] if t["hp"] > 0]
    game["enemies"] = [e for e in game["enemies"] if not e.get("dead")]
    # wave clear
    if not game["spawnQueue"] and not game["enemies"] and game["phase"] == "combat":
        game["gold"] += 15+6*game["level"]
        for t in game["grid"]:
            t["hp"] = min(TOWERS[t["type"]]["hp"], t["hp"]+TOWERS[t["type"]]["hp"]*0.10)
        if game["wave"] >= game["level"]+2:
            next_level()
        else:
            game["phase"] = "build"
            game["event_id"] += 1
            game["last_wave"] = {"level": game["level"], "wave": 0, "id": game["event_id"]}

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
                    game["level"] = 0; game["wave"] = 0
                    game["grid"] = []; game["gold"] = 90; game["base"] = MAXBASE
                    game["enemies"] = []; game["spawnQueue"] = []
                    game["winner"] = 0
                    next_level()
                    game["last_over"] = None
                step(dt)
        time.sleep(1/60)

def snapshot():
    cd = 0
    if game["phase"] == "countdown": cd = max(1, math.ceil(game["countdown_end"]-now()))
    return {
        "v": VERSION,
        "phase": game["phase"],
        "countdown": cd,
        "level": game["level"], "wave": game["wave"], "wavesTotal": game["level"]+2,
        "gold": game["gold"], "base": game["base"],
        "grid": [{"c": t["c"], "r": t["r"], "type": t["type"],
                  "hp": round(t["hp"], 1), "max": TOWERS[t["type"]]["hp"]} for t in game["grid"]],
        "enemies": [{"x": round(e["x"], 1), "y": round(e["y"], 1),
                     "hp": round(max(0, e["hp"]), 1), "maxhp": e["maxhp"],
                     "kind": e["kind"]} for e in game["enemies"]],
        "players": [{"x": round(p["x"], 1), "y": round(p["y"], 1), "sel": p["sel"]}
                    if p else None for p in game["players"]],
        "names": [((game["players"][i] or {}).get("name", "") or "") for i in range(MAX_SEATS)],
        "ready": [bool(game["players"][i] and game["players"][i]["ready"]) for i in range(MAX_SEATS)],
        "connected": [bool(game["players"][i]) for i in range(MAX_SEATS)],
        "last_wave": game["last_wave"],
        "last_over": game["last_over"],
        "last_build": game["last_build"],
        "paused": game["paused"],
        "paused_by": game["paused_by"],
        "event_id": game["event_id"],
    }

class Handler(SimpleHTTPRequestHandler):
    protocol_version = "HTTP/1.1"  # keep-alive: no TCP+TLS handshake per poll
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
                                            "input":{"x":0,"y":0},"sel":0,"build":False,
                                            "x":120+s*120,"y":540}
                    else:
                        game["players"][s]["name"]=name; game["players"][s]["last_seen"]=now()
                    return self._json({"you":s+1})
                touch(pid); s=slot_of(pid)
                if self.path=="/api/input" and s>=0:
                    try:
                        ix=max(-1,min(1,float(d.get("x",0)))); iy=max(-1,min(1,float(d.get("y",0))))
                    except: ix,iy=0,0
                    game["players"][s]["input"]={"x":ix,"y":iy}
                    try: game["players"][s]["sel"]=int(d.get("sel",game["players"][s]["sel"]))%3
                    except: pass
                    if d.get("build"): game["players"][s]["build"]=True
                    return self._json({"ok":True})
                if self.path=="/api/wave" and s>=0:
                    return self._json({"ok":start_wave()})
                if self.path=="/api/ready" and s>=0:
                    game["players"][s]["ready"]=True
                    return self._json({"ok":True})
                if self.path=="/api/leave":
                    if s>=0:
                        game["players"][s]=None
                    return self._json({"ok":True})
                if self.path=="/api/pause":
                    # pause-vote: any defender freezes the sim for all; any resume thaws deadlines
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
                        for p in game["players"]:
                            if p: p["ready"]=False
                        game["phase"]="waiting"; game["winner"]=0
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
    threading.Thread(target=game_loop,daemon=True).start()
    srv=ThreadingHTTPServer(("0.0.0.0",PORT),Handler)
    srv.socket.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)  # no Nagle delay on small packets
    print(f"\n  BASTION LAN running! 2-4 defenders hold the road.\n  On this laptop:  http://localhost:{PORT}")
    _ips = lan_ips()
    for ip in _ips: print(f"  Friend on same WiFi:  http://{ip}:{PORT}")
    if _ips:
        print("  Scan to join (same WiFi):")
        print_qr(f"http://{_ips[0]}:{PORT}")
    print(f"\n  Stand on grass + build key. Mid-wave builds +25%. Repair at 50%.\n")
    try: srv.serve_forever()
    except KeyboardInterrupt: print("\nbye!")
