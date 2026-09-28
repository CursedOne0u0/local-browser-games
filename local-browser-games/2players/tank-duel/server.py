#!/usr/bin/env python3
"""Tank Duel — LAN 2-player maze tanks. Zero dependencies, stdlib only.
Run:  python3 server.py   (both players open the printed LAN URL, same WiFi)
"""
import json, time, math, random, threading, socket, os
from http.server import ThreadingHTTPServer, SimpleHTTPRequestHandler
from pathlib import Path
import urllib.parse

PORT = int(os.environ.get("PORT", "3001"))
VERSION = "1.8"  # bump on every update; shown on the site
WIN_ROUNDS = 5
PUBLIC = Path(__file__).parent / "public"

CLASSIC_MAZE = [
    "#################",
    "#.......#.......#",
    "#.#####.#.#####.#",
    "#.#...#...#...#.#",
    "#.#.#.#####.#.#.#",
    "#...#.......#...#",
    "#...#.......#...#",
    "#.#.#.#####.#.#.#",
    "#.#...#...#...#.#",
    "#.#####.#.#####.#",
    "#.......#.......#",
    "#################",
]
MAZE = list(CLASSIC_MAZE)  # live map: regenerated every round by gen_maze()
COLS, ROWS, CELL = len(MAZE[0]), len(MAZE), 60
W, H = COLS*CELL, ROWS*CELL
TANK_R = 16
TANK_SPEED = 175
TURN_SPEED = 3.4
BULLET_SPEED = 430
FIRE_CD = 0.4
RAPID_CD = 0.12
MAX_BULLETS = 5
BULLET_BOUNCES = 6
BULLET_LIFE = 5.0
OWNER_GRACE = 0.4
WEAPONS = {"spread": 10.0, "rapid": 8.0, "shield": 12.0,
           "homing": 9.0, "mines": 14.0, "rail": 8.0}
MAX_PICKUPS = 2
MAX_MINES = 3
MINE_ARM = 1.0
MINE_RADIUS = 44
RAIL_CHARGE = 0.9
RAIL_RANGE = 950
RAIL_HALFW = 24

lock = threading.Lock()
game = {
    "phase": "waiting",  # waiting|countdown|playing|round|over
    "countdown_end": 0,
    "round_end": 0,
    "round_scored": 0,
    "round": 1,
    "players": [None, None],  # {id,name,last_seen,ready,input:{fwd,turn},fire,prev_fire}
    "tanks": [{"x": 0, "y": 0, "ang": 0, "score": 0, "alive": True, "cd": 0, "wpn": None, "wpn_until": 0, "rail_charge": 0, "rail_ang": 0},
              {"x": 0, "y": 0, "ang": 0, "score": 0, "alive": True, "cd": 0, "wpn": None, "wpn_until": 0, "rail_charge": 0, "rail_ang": 0}],
    "bullets": [],  # {x,y,vx,vy,owner,bounces,born,grace,home,rail,life}
    "mines": [],  # {x,y,owner,armed_at}
    "pickups": [],  # {x,y,kind,born}
    "pickup_timer": 5.0,
    "winner": 0,
    "event_id": 0,
    "last_kill": None,  # {by,victim,suicide,id}
    "last_block": None,  # {tank,id} shield save
    "last_pickup": None,  # {tank,kind,id}
    "last_shot": None,  # {by,kind,id}
    "last_beam": None,  # {x1,y1,x2,y2,by,id}
    "last_pop": None,  # {x,y,id} bullet shattered on final bounce
    "bounce_n": 0,
}
SPAWNS = [
    {"cx": 1.5, "cy": 10.5, "ang": -math.pi/2},
    {"cx": 15.5, "cy": 1.5, "ang": math.pi/2},
]
SPAWN_CELLS = [(1, ROWS-2), (COLS-2, 1)]  # 180° rotational mirrors of each other

def gen_maze():
    """Random 180°-symmetric maze, BFS-verified traversable between spawns.

    Fair by construction (spawns are rotational mirrors, walls mirrored too).
    Falls back to CLASSIC_MAZE if no good layout in 200 tries.
    """
    def mirror(c, r): return (COLS-1-c, ROWS-1-r)
    for _ in range(200):
        g = [['#']*COLS for _ in range(ROWS)]
        for r in range(1, ROWS-1):
            for c in range(1, COLS-1):
                mc, mr = mirror(c, r)
                if (c, r) > (mc, mr): continue  # fill each mirror pair once
                ch = '#' if random.random() < 0.30 else '.'
                g[r][c] = g[mr][mc] = ch
        # carve maneuvering room around both spawns (border cells skipped)
        for cc, rr in SPAWN_CELLS:
            for dc in range(-1, 2):
                for dr in range(-1, 2):
                    c2, r2 = cc+dc, rr+dr
                    if 1 <= c2 < COLS-1 and 1 <= r2 < ROWS-1:
                        g[r2][c2] = '.'
        # openness guard: claustrophobic maps aren't fun
        if sum(row.count('.') for row in g) < (COLS-2)*(ROWS-2)*0.55:
            continue
        # BFS: spawn1 must reach spawn2 over open cells (4-connectivity)
        s1, s2 = SPAWN_CELLS
        seen, stack = {s1}, [s1]
        while stack:
            c, r = stack.pop()
            for dc, dr in ((1, 0), (-1, 0), (0, 1), (0, -1)):
                n = (c+dc, r+dr)
                if (0 <= n[0] < COLS and 0 <= n[1] < ROWS
                        and g[n[1]][n[0]] == '.' and n not in seen):
                    seen.add(n); stack.append(n)
        if s2 in seen:
            return [''.join(row) for row in g]
    return list(CLASSIC_MAZE)

def now(): return time.time()

def is_wall(px, py):
    c, r = int(px/CELL), int(py/CELL)
    if c < 0 or r < 0 or c >= COLS or r >= ROWS: return True
    return MAZE[r][c] == "#"

def circle_free(x, y, rad=TANK_R):
    for ox, oy in ((-rad, 0), (rad, 0), (0, -rad), (0, rad),
                   (-rad*0.7, -rad*0.7), (rad*0.7, rad*0.7),
                   (-rad*0.7, rad*0.7), (rad*0.7, -rad*0.7)):
        if is_wall(x+ox, y+oy): return False
    return True

def reset_tanks():
    global MAZE
    MAZE = gen_maze()  # fresh random (but verified traversable) map every round
    for i, s in enumerate(SPAWNS):
        t = game["tanks"][i]
        t["x"] = s["cx"]*CELL; t["y"] = s["cy"]*CELL
        t["ang"] = s["ang"]; t["alive"] = True; t["cd"] = 0
        t["wpn"] = None; t["wpn_until"] = 0
        t["rail_charge"] = 0; t["rail_ang"] = 0
    game["bullets"] = []
    game["mines"] = []
    game["pickups"] = []
    game["pickup_timer"] = 5.0

def open_cell():
    for _ in range(40):
        c = random.randrange(1, COLS-1); r = random.randrange(1, ROWS-1)
        if MAZE[r][c] != ".": continue
        x, y = (c+0.5)*CELL, (r+0.5)*CELL
        if all(math.hypot(x-t["x"], y-t["y"]) > 3*CELL for t in game["tanks"]):
            return x, y
    return None

def spawn_pickup():
    if len(game["pickups"]) >= MAX_PICKUPS: return
    spot = open_cell()
    if not spot: return
    game["pickups"].append({"x": spot[0], "y": spot[1],
                            "kind": random.choice(list(WEAPONS)),
                            "born": now()})

def reset_scores():
    for t in game["tanks"]: t["score"] = 0
    game["winner"] = 0; game["round"] = 1

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

def fire_bullet(i):
    t = game["tanks"][i]
    if not t["alive"] or t["cd"] > 0: return
    wpn = t["wpn"] if now() < t["wpn_until"] else None
    if wpn == "mines":  # lay a mine instead of firing
        if sum(1 for m in game["mines"] if m["owner"] == i) >= MAX_MINES: return
        t["cd"] = 0.8
        game["event_id"] += 1
        game["last_shot"] = {"by": i+1, "id": game["event_id"]}
        game["mines"].append({"x": t["x"]-math.cos(t["ang"])*(TANK_R+6),
                              "y": t["y"]-math.sin(t["ang"])*(TANK_R+6),
                              "owner": i, "armed_at": now()+MINE_ARM})
        return
    active = sum(1 for b in game["bullets"] if b["owner"] == i)
    shots = [0.0] if wpn != "spread" else (-0.26, 0.0, 0.26)
    shots = shots[:max(1, MAX_BULLETS-active)]
    if not shots: return
    t["cd"] = RAPID_CD if wpn == "rapid" else FIRE_CD
    game["event_id"] += 1
    if wpn == "rail":  # telegraphed charge: locks aim, beam fires later
        t["rail_charge"] = RAIL_CHARGE; t["rail_ang"] = t["ang"]
        t["cd"] = RAIL_CHARGE+0.5
        game["last_shot"] = {"by": i+1, "kind": "charge", "id": game["event_id"]}
        return
    game["last_shot"] = {"by": i+1, "kind": "shell", "id": game["event_id"]}
    spd = BULLET_SPEED
    for off in shots:
        a = t["ang"]+off
        game["bullets"].append({
            "x": t["x"]+math.cos(a)*(TANK_R+8), "y": t["y"]+math.sin(a)*(TANK_R+8),
            "vx": math.cos(a)*spd, "vy": math.sin(a)*spd,
            "owner": i, "bounces": 0, "born": now(), "grace": now()+OWNER_GRACE,
            "home": wpn == "homing",
        })

def fire_rail(i):
    # hitscan beam along the locked aim: stops at first wall, hits nearest tank
    t = game["tanks"][i]
    ang = t.get("rail_ang", t["ang"])
    x1, y1 = t["x"], t["y"]
    dx, dy = math.cos(ang), math.sin(ang)
    d = 8.0
    while d < RAIL_RANGE and not is_wall(x1+dx*d, y1+dy*d):
        d += 8.0
    best, bestt, hx, hy = None, None, x1+dx*d, y1+dy*d
    for j in (0, 1):
        if j == i: continue
        o = game["tanks"][j]
        if not o["alive"]: continue
        tt = (o["x"]-x1)*dx + (o["y"]-y1)*dy
        if tt < 0 or tt > d: continue
        dd = math.hypot(o["x"]-(x1+dx*tt), o["y"]-(y1+dy*tt))
        if dd < TANK_R+RAIL_HALFW and (bestt is None or tt < bestt):
            best, bestt, hx, hy = j, tt, x1+dx*tt, y1+dy*tt
    game["event_id"] += 1
    game["last_beam"] = {"x1": round(x1,1), "y1": round(y1,1),
                         "x2": round(hx,1), "y2": round(hy,1),
                         "by": i+1, "id": game["event_id"]}
    if best is not None:
        o = game["tanks"][best]
        if o["wpn"] == "shield" and now() < o["wpn_until"]:
            o["wpn"] = None; o["wpn_until"] = 0
            game["event_id"] += 1
            game["last_block"] = {"tank": best+1, "id": game["event_id"]}
        else:
            kill(best, i)

def kill(victim, by):
    game["tanks"][victim]["alive"] = False
    scorer = 1-victim if victim == by else by  # suicide awards the foe
    game["tanks"][scorer]["score"] += 1
    game["bullets"] = []
    game["event_id"] += 1
    game["last_kill"] = {"by": scorer+1, "victim": victim+1, "suicide": victim == by, "id": game["event_id"]}
    if game["tanks"][scorer]["score"] >= WIN_ROUNDS:
        game["phase"] = "over"; game["winner"] = scorer+1
    else:
        game["phase"] = "round"; game["round_scored"] = scorer+1
        game["round_end"] = now()+2.0

def step(dt):
    if game["phase"] == "countdown" and now() >= game["countdown_end"]:
        game["phase"] = "playing"
        return
    if game["phase"] == "round" and now() >= game["round_end"]:
        game["round"] += 1
        reset_tanks()
        game["phase"] = "playing"
        return
    if game["phase"] != "playing": return
    # tanks
    for i in (0, 1):
        t = game["tanks"][i]
        if not t["alive"]: continue
        t["cd"] = max(0, t["cd"]-dt)
        if t.get("rail_charge", 0) > 0:  # railgun charge ticks down in game time
            t["rail_charge"] -= dt
            if t["rail_charge"] <= 0:
                t["rail_charge"] = 0
                if t["alive"]:
                    fire_rail(i)
        p = game["players"][i]
        fwd = turn = 0
        if p:
            fwd = max(-1, min(1, p["input"]["fwd"]))
            turn = max(-1, min(1, p["input"]["turn"]))
            rapid = t["wpn"] == "rapid" and now() < t["wpn_until"]
            if p["fire"] and (not p["prev_fire"] or rapid):
                fire_bullet(i)
            p["prev_fire"] = p["fire"]
        t["ang"] += turn*TURN_SPEED*dt
        nx = t["x"]+math.cos(t["ang"])*fwd*TANK_SPEED*dt
        if circle_free(nx, t["y"]): t["x"] = nx
        ny = t["y"]+math.sin(t["ang"])*fwd*TANK_SPEED*dt
        if circle_free(t["x"], ny): t["y"] = ny
    # bullets (substepped; axis-separated bounce)
    for b in game["bullets"]:
        if b.get("home"):  # homing shells steer at the foe while armed
            foe = game["tanks"][1-b["owner"]]
            ot = game["tanks"][b["owner"]]
            if foe["alive"] and ot["wpn"] == "homing" and now() < ot["wpn_until"]:
                sp = math.hypot(b["vx"], b["vy"]) or 1
                cur = math.atan2(b["vy"], b["vx"])
                want = math.atan2(foe["y"]-b["y"], foe["x"]-b["x"])
                dd = (want-cur+math.pi) % (2*math.pi) - math.pi
                na = cur + max(-2.8*dt, min(2.8*dt, dd))
                b["vx"] = math.cos(na)*sp; b["vy"] = math.sin(na)*sp
        sdt = dt/3
        for _ in range(3):
            oldx, oldy = b["x"], b["y"]
            b["x"] += b["vx"]*sdt; b["y"] += b["vy"]*sdt
            if is_wall(b["x"], b["y"]):
                if not is_wall(oldx, b["y"]):
                    b["x"] = oldx; b["vx"] *= -1
                elif not is_wall(b["x"], oldy):
                    b["y"] = oldy; b["vy"] *= -1
                else:
                    b["x"], b["y"] = oldx, oldy
                    b["vx"] *= -1; b["vy"] *= -1
                b["bounces"] += 1
                game["bounce_n"] += 1
                if b["bounces"] > BULLET_BOUNCES:  # shatters: report break point
                    b["dead"] = True
                    game["event_id"] += 1
                    game["last_pop"] = {"x": round(b["x"],1), "y": round(b["y"],1),
                                        "id": game["event_id"]}
                break
        # hits (owner immune during grace; live shield absorbs once)
        for i in (0, 1):
            t = game["tanks"][i]
            if not t["alive"]: continue
            if i == b["owner"] and now() < b["grace"]: continue
            if math.hypot(b["x"]-t["x"], b["y"]-t["y"]) < TANK_R+5:
                b["dead"] = True
                if t["wpn"] == "shield" and now() < t["wpn_until"]:
                    t["wpn"] = None; t["wpn_until"] = 0
                    game["event_id"] += 1
                    game["last_block"] = {"tank": i+1, "id": game["event_id"]}
                else:
                    kill(i, b["owner"])
                break
    game["bullets"] = [b for b in game["bullets"]
                       if not b.get("dead") and b["bounces"] <= BULLET_BOUNCES
                       and now()-b["born"] < BULLET_LIFE]
    # mines: arm, then splash foe first, owner second
    for m in game["mines"]:
        if now() < m["armed_at"]: continue
        for i in (1-m["owner"], m["owner"]):
            t = game["tanks"][i]
            if t["alive"] and math.hypot(m["x"]-t["x"], m["y"]-t["y"]) < MINE_RADIUS:
                m["dead"] = True
                kill(i, m["owner"])
                break
        if game["phase"] != "playing": break
    game["mines"] = [m for m in game["mines"] if not m.get("dead")]
    # pickups: spawn over time, collect on touch, expire
    game["pickup_timer"] -= dt
    if game["pickup_timer"] <= 0:
        spawn_pickup()
        game["pickup_timer"] = 6+random.random()*4
    for pk in game["pickups"]:
        for i in (0, 1):
            t = game["tanks"][i]
            if t["alive"] and math.hypot(pk["x"]-t["x"], pk["y"]-t["y"]) < 24:
                t["wpn"] = pk["kind"]; t["wpn_until"] = now()+WEAPONS[pk["kind"]]
                pk["dead"] = True
                game["event_id"] += 1
                game["last_pickup"] = {"tank": i+1, "kind": pk["kind"], "id": game["event_id"]}
                break
    game["pickups"] = [pk for pk in game["pickups"]
                       if not pk.get("dead") and now()-pk["born"] < 15]
    for t in game["tanks"]:
        if t["wpn"] and now() >= t["wpn_until"]:
            t["wpn"] = None

def game_loop():
    last = time.time()
    while True:
        t = time.time(); dt = min(0.05, t-last); last = t
        with lock:
            free_stale()
            p0, p1 = game["players"]
            if game["phase"] == "waiting" and p0 and p1 and p0["ready"] and p1["ready"]:
                game["phase"] = "countdown"; game["countdown_end"] = now()+2.4
                reset_scores(); reset_tanks()
            step(dt)
        time.sleep(1/60)

def snapshot():
    cd = 0
    if game["phase"] == "countdown": cd = max(1, math.ceil(game["countdown_end"]-now()))
    return {
        "v": VERSION,
        "phase": game["phase"],
        "countdown": cd,
        "round": game["round"],
        "maze": MAZE,
        "tanks": [{"x": round(t["x"],1), "y": round(t["y"],1), "ang": round(t["ang"],3),
                   "score": t["score"], "alive": t["alive"],
                   "wpn": t["wpn"] if now() < t["wpn_until"] else None,
                   "wpn_t": round(max(0, t["wpn_until"]-now()),1),
                   "rail": ({"t": round(t.get("rail_charge", 0),2), "ang": round(t["rail_ang"],3)}
                            if t.get("rail_charge", 0) > 0 else None)}
                  for t in game["tanks"]],
        "bullets": [{"x": round(b["x"],1), "y": round(b["y"],1)} for b in game["bullets"]],
        "mines": [{"x": round(m["x"],1), "y": round(m["y"],1), "owner": m["owner"],
                   "armed": now() >= m["armed_at"]} for m in game["mines"]],
        "pickups": [{"x": round(p["x"],1), "y": round(p["y"],1), "kind": p["kind"]} for p in game["pickups"]],
        "last_block": game["last_block"],
        "last_pickup": game["last_pickup"],
        "last_shot": game["last_shot"],
        "last_beam": game["last_beam"],
        "last_pop": game["last_pop"],
        "bounce_n": game["bounce_n"],
        "winner": game["winner"],
        "names": [(game["players"][0] or {}).get("name","") or "", (game["players"][1] or {}).get("name","") or ""],
        "ready": [bool(game["players"][0] and game["players"][0]["ready"]), bool(game["players"][1] and game["players"][1]["ready"])],
        "connected": [bool(game["players"][0]), bool(game["players"][1])],
        "last_kill": game["last_kill"],
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
                                            "input":{"fwd":0,"turn":0},"fire":False,"prev_fire":False}
                    else:
                        game["players"][s]["name"]=name; game["players"][s]["last_seen"]=now()
                    return self._json({"you":s+1})
                touch(pid); s=slot_of(pid)
                if self.path=="/api/input" and s>=0:
                    try:
                        f=max(-1,min(1,float(d.get("fwd",0)))); u=max(-1,min(1,float(d.get("turn",0))))
                    except: f,u=0,0
                    game["players"][s]["input"]={"fwd":f,"turn":u}
                    game["players"][s]["fire"]=bool(d.get("fire",False))
                    return self._json({"ok":True})
                if self.path=="/api/ready" and s>=0:
                    game["players"][s]["ready"]=True
                    return self._json({"ok":True})
                if self.path=="/api/leave":
                    if s>=0:  # browser closed: free the seat now, don't wait out the 8s timeout
                        game["players"][s]=None
                    return self._json({"ok":True})
                if self.path=="/api/restart":
                    if game["phase"]=="over":
                        reset_scores(); reset_tanks()
                        for p in game["players"]:
                            if p: p["ready"]=False
                        game["phase"]="waiting"; game["winner"]=0; game["round"]=1
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
    reset_tanks()
    threading.Thread(target=game_loop,daemon=True).start()
    srv=ThreadingHTTPServer(("0.0.0.0",PORT),Handler)
    srv.socket.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)  # no Nagle delay on small packets
    print(f"\n  TANK DUEL running!\n  On this laptop:  http://localhost:{PORT}")
    for ip in lan_ips(): print(f"  Friend on same WiFi:  http://{ip}:{PORT}")
    print(f"\n  Controls: W/S drive, A/D rotate, SPACE fire (arrows work too)\n  First to {WIN_ROUNDS} rounds wins!\n")
    try: srv.serve_forever()
    except KeyboardInterrupt: print("\nbye!")
