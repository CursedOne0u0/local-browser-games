#!/usr/bin/env python3
"""Echo Hunt — LAN 2-8 player sonar tag. Zero dependencies, stdlib only.
Run:  python3 server.py   (every player opens the printed LAN URL, same WiFi)
Wardens hunt near-blind on sonar; divers see wide and crack wire nodes.
"""
import json, time, math, random, threading, socket, os
from http.server import ThreadingHTTPServer, SimpleHTTPRequestHandler
from pathlib import Path
import urllib.parse

PORT = int(os.environ.get("PORT", "3003"))
VERSION = "1.3"  # bump on every update; shown on the site
MAX_PLAYERS = 8
WIN_ROUNDS = 3
PUBLIC = Path(__file__).parent / "public"

W, H = 2200, 1400
RUN_R = 15
DIVER_SPEED = 300
HUNTER_SPEED = 270
TAG_DIST = 34
NODE_R = 26
CHANNEL_TIME = 2.0  # touch a node this long to claim a puzzle (silent)
LIVE_NODES = 3
SPAWN_CLEAR_HUNTER = 350
NODE_SPREAD = 300
ROUND_TIME = 180
PING_EVERY = 4.0
BOT_NAMES = ["Byte", "Echo", "Sonar", "Pixel", "Glitch", "Watt", "Ping", "Fathom"]
BOT_SOLVE_MIN, BOT_SOLVE_MAX = 5.0, 9.0  # bot "thinking" time per puzzle
PICK_COLORS = ["red", "orange", "yellow", "green", "cyan", "blue", "purple", "pink"]
# trench furniture: 180-degree rotational mirrors (x,y,w,h) <-> (W-x-w,H-y-h)
PILLARS = [
    {"x": 300, "y": 250, "w": 80, "h": 80},
    {"x": 1820, "y": 1070, "w": 80, "h": 80},
    {"x": 700, "y": 900, "w": 120, "h": 60},
    {"x": 1380, "y": 440, "w": 120, "h": 60},
    {"x": 1050, "y": 200, "w": 60, "h": 140},
    {"x": 1090, "y": 1060, "w": 60, "h": 140},
    {"x": 1500, "y": 700, "w": 100, "h": 100},
    {"x": 600, "y": 600, "w": 100, "h": 100},
    {"x": 350, "y": 1050, "w": 140, "h": 70},
    {"x": 1710, "y": 280, "w": 140, "h": 70},
    {"x": 950, "y": 600, "w": 70, "h": 200},
    {"x": 1180, "y": 600, "w": 70, "h": 200},
    {"x": 1800, "y": 600, "w": 80, "h": 160},
    {"x": 320, "y": 640, "w": 80, "h": 160},
]
DIVER_SPAWNS = [(150, 150), (2050, 150), (150, 1250), (2050, 1250),
                (150, 700), (2050, 700), (1100, 150), (1100, 1250)]
HUNTER_SPAWNS = [(1100, 700), (1070, 660), (1130, 660), (1070, 740), (1130, 740)]

lock = threading.Lock()
game = {
    "phase": "waiting",  # waiting|countdown|playing|round|over
    "countdown_end": 0,
    "round_end": 0,
    "round": 1,
    "h_wins": 0, "d_wins": 0,
    "winner_side": 0,  # 0 none, 1 hunters, 2 divers
    "players": [None]*MAX_PLAYERS,  # {id,name,last_seen,ready,input:{x,y},bot?}
    "bots": {},  # seat -> {solve_at,wp,wp_at} brain scratch (bot seats only)
    "ips": {},  # clientId -> last seen IP (localhost check = host controls)
    "runners": [{"x": 0, "y": 0, "alive": True, "stam": 1.0, "gassed": False} for _ in range(MAX_PLAYERS)],
    "effmag": [0.0]*MAX_PLAYERS,  # post-stamina-cap speed actually applied (drives blips)
    "roles": [None]*MAX_PLAYERS,  # hunter|diver|None per seat
    "hunt_counts": {},  # clientId -> rounds spent as hunter (fair rotation)
    "channel": [None]*MAX_PLAYERS,  # {node,since} while touching a node
    "nodes": [],  # {x,y,done,slots:[{solver,resolved,n,src,dst,perm}]}
    "target": 6,
    "cracked": 0,
    "rings": [],  # {x,y,born} sonar pulses (audible = visible to all)
    "blips": [],  # {x,y,str,until} hunter contacts
    "next_ping_at": 0,
    "round_time_end": 0,
    "event_id": 0,
    "last_ping": None,  # {id}
    "last_shout": None,  # {x,y,by,id} puzzle claim/solve/collect noise
    "last_tag": None,  # {hunter,victim,id}
    "last_collect": None,  # {by,done,target,id} node cracked
    "last_round": None,  # {side,reason,id}
}

def now(): return time.time()

def touch(pid):
    for p in game["players"]:
        if p and p["id"] == pid: p["last_seen"] = now()
def slot_of(pid):
    for i, p in enumerate(game["players"]):
        if p and p["id"] == pid: return i
    return -1
def free_stale():
    for i, p in enumerate(game["players"]):
        if p and not p.get("bot") and now() - p["last_seen"] > 8:
            release_slots(i)
            game["players"][i] = None
            game["roles"][i] = None
            game["channel"][i] = None

def collide(x, y):
    x = max(RUN_R, min(W-RUN_R, x)); y = max(RUN_R, min(H-RUN_R, y))
    for pl in PILLARS:
        cx = max(pl["x"], min(pl["x"]+pl["w"], x))
        cy = max(pl["y"], min(pl["y"]+pl["h"], y))
        dx, dy = x-cx, y-cy
        d2 = dx*dx+dy*dy
        if d2 < RUN_R*RUN_R:
            if d2 > 1e-6:
                d = math.sqrt(d2)
                x = cx+dx/d*RUN_R; y = cy+dy/d*RUN_R
            else:
                l, r, t, b = x-pl["x"], pl["x"]+pl["w"]-x, y-pl["y"], pl["y"]+pl["h"]-y
                m = min(l, r, t, b)
                if m == l: x = pl["x"]-RUN_R
                elif m == r: x = pl["x"]+pl["w"]+RUN_R
                elif m == t: y = pl["y"]-RUN_R
                else: y = pl["y"]+pl["h"]+RUN_R
    return x, y

def point_clear(x, y, margin=30):
    if not (margin < x < W-margin and margin < y < H-margin): return False
    return all(not (pl["x"]-margin < x < pl["x"]+pl["w"]+margin and
                    pl["y"]-margin < y < pl["y"]+pl["h"]+margin) for pl in PILLARS)

def hunters():
    return [i for i, r in enumerate(game["roles"]) if r == "hunter"]
def divers():
    return [i for i, r in enumerate(game["roles"]) if r == "diver"]

def release_slots(seat):
    for nd in game["nodes"]:
        for sl in nd["slots"]:
            if sl["solver"] == seat:
                sl["solver"] = None
    game["channel"][seat] = None

def holds_slot(seat):
    return any(sl["solver"] == seat for nd in game["nodes"] for sl in nd["slots"])

def make_puzzle():
    k = random.randrange(3, 9)  # 3-8 color pairs
    cols = random.sample(PICK_COLORS, k)
    perm = list(range(k)); random.shuffle(perm)
    return {"solver": None, "resolved": False, "n": k,
            "src": list(cols), "dst": [cols[perm[d]] for d in range(k)],
            "perm": perm}

def spawn_node():
    hunters_pos = [(game["runners"][i]["x"], game["runners"][i]["y"]) for i in hunters()]
    for _ in range(120):
        x = 70+random.random()*(W-140); y = 70+random.random()*(H-140)
        if not point_clear(x, y): continue
        if hunters_pos and min(math.hypot(x-hx, y-hy) for hx, hy in hunters_pos) < SPAWN_CLEAR_HUNTER:
            continue
        if any(math.hypot(x-nd["x"], y-nd["y"]) < NODE_SPREAD for nd in game["nodes"]):
            continue
        game["nodes"].append({"x": x, "y": y, "done": 0,
                              "slots": [make_puzzle() for _ in range(3)]})
        return

def reset_positions():
    hi, di = 0, 0
    for i in range(MAX_PLAYERS):
        r = game["runners"][i]
        if game["roles"][i] == "hunter":
            s = HUNTER_SPAWNS[hi % len(HUNTER_SPAWNS)]; hi += 1
        elif game["roles"][i] == "diver":
            s = DIVER_SPAWNS[di % len(DIVER_SPAWNS)]; di += 1
        else:
            continue
        r["x"], r["y"] = s; r["alive"] = True; r["stam"] = 1.0; r["gassed"] = False
        game["channel"][i] = None; game["effmag"][i] = 0.0

def assign_roles():
    seated = [i for i, p in enumerate(game["players"]) if p]
    n = len(seated)
    nh = max(1, round(n/3))
    order = sorted(seated, key=lambda i: (game["hunt_counts"].get(game["players"][i]["id"], 0),
                                          random.random()))
    game["roles"] = [None]*MAX_PLAYERS
    for i in order[:nh]:
        game["roles"][i] = "hunter"
        pid = game["players"][i]["id"]
        game["hunt_counts"][pid] = game["hunt_counts"].get(pid, 0)+1
    for i in order[nh:]:
        game["roles"][i] = "diver"
    game["target"] = 4+len(order[nh:])
    game["cracked"] = 0
    game["nodes"] = []
    game["rings"] = []; game["blips"] = []
    reset_positions()
    for _ in range(LIVE_NODES):
        spawn_node()

def reset_match():
    game["h_wins"] = 0; game["d_wins"] = 0; game["winner_side"] = 0
    game["round"] = 1
    game["hunt_counts"] = {}
    game["last_tag"] = None; game["last_collect"] = None
    game["last_round"] = None; game["last_shout"] = None

def shout(x, y, by):
    game["event_id"] += 1
    game["last_shout"] = {"x": round(x, 1), "y": round(y, 1), "by": by,
                          "id": game["event_id"]}
    game["blips"].append({"x": x, "y": y, "str": 1.2, "until": now()+2.5})

def win_round(side, reason):
    if side == 1: game["h_wins"] += 1
    else: game["d_wins"] += 1
    game["event_id"] += 1
    game["last_round"] = {"side": side, "reason": reason, "id": game["event_id"]}
    if game["h_wins"] >= WIN_ROUNDS or game["d_wins"] >= WIN_ROUNDS:
        game["phase"] = "over"
        game["winner_side"] = 1 if game["h_wins"] >= WIN_ROUNDS else 2
    else:
        game["phase"] = "round"; game["round_end"] = now()+2.5

def sonar_ping():
    t = now()
    game["next_ping_at"] = t+PING_EVERY
    for i in hunters():
        r = game["runners"][i]
        game["rings"].append({"x": r["x"], "y": r["y"], "born": t})
    for i in divers():
        r = game["runners"][i]
        if not r["alive"]: continue
        mag = game["effmag"][i]
        if mag < 0.15 or holds_slot(i):
            continue  # still or buried in a puzzle: invisible
        bright = mag >= 0.6
        game["blips"].append({"x": r["x"], "y": r["y"],
                              "str": 1.0 if bright else 0.5,
                              "until": t+(2.5 if bright else 1.0)})
    game["event_id"] += 1
    game["last_ping"] = {"id": game["event_id"]}

def step(dt):
    if game["phase"] == "countdown" and now() >= game["countdown_end"]:
        game["phase"] = "playing"
        game["round_time_end"] = now()+ROUND_TIME
        game["next_ping_at"] = now()+2.0  # opening grace before first ping
        return
    if game["phase"] == "round" and now() >= game["round_end"]:
        game["round"] += 1
        assign_roles()
        game["phase"] = "playing"
        game["round_time_end"] = now()+ROUND_TIME
        game["next_ping_at"] = now()+2.0
        return
    if game["phase"] != "playing": return
    t = now()
    bot_brain(dt)
    if t >= game["next_ping_at"]:
        sonar_ping()
    # movement (divers capped by stamina, hunters flat)
    for i in range(MAX_PLAYERS):
        role = game["roles"][i]
        if not role: continue
        r = game["runners"][i]
        if role == "diver" and not r["alive"]: continue
        p = game["players"][i]
        ix = iy = 0
        if p:
            ix = max(-1, min(1, p["input"]["x"])); iy = max(-1, min(1, p["input"]["y"]))
        mag = math.hypot(ix, iy)
        if mag > 1: ix /= mag; iy /= mag; mag = 1
        if role == "diver":
            if mag > 0.6:
                r["stam"] = max(0, r["stam"]-0.25*dt)
            elif mag < 0.35:
                r["stam"] = min(1, r["stam"]+0.18*dt)
                if r["stam"] >= 0.3: r["gassed"] = False
            if r["stam"] <= 0: r["gassed"] = True
            if r["gassed"] and mag > 0.3:  # gassed: forced stroll until recovery
                mag = 0.3  # cap the amount only; ix,iy already carry direction
            spd = DIVER_SPEED*mag
        else:
            spd = HUNTER_SPEED*mag
        game["effmag"][i] = mag
        # move cancels puzzle-solving (silent abandon)
        if holds_slot(i) and mag > 0.15:
            release_slots(i)
        x, y = r["x"]+ix*spd*dt, r["y"]+iy*spd*dt
        r["x"], r["y"] = collide(x, y)
    # node channeling: alive divers touching a node with a free slot
    for i in divers():
        r = game["runners"][i]
        if not r["alive"] or holds_slot(i):
            game["channel"][i] = None
            continue
        found = None
        for ni, nd in enumerate(game["nodes"]):
            if nd["done"] >= 3: continue
            if not any(sl["solver"] is None and not sl["resolved"] for sl in nd["slots"]):
                continue
            if math.hypot(r["x"]-nd["x"], r["y"]-nd["y"]) < NODE_R:
                found = ni
                break
        ch = game["channel"][i]
        if found is None:
            game["channel"][i] = None
            continue
        if not ch or ch["node"] != found:
            game["channel"][i] = {"node": found, "since": t}
            continue
        if t-ch["since"] >= CHANNEL_TIME:
            nd = game["nodes"][found]
            sl = next(s for s in nd["slots"] if s["solver"] is None and not s["resolved"])
            sl["solver"] = i
            game["channel"][i] = None
            if (game["players"][i] or {}).get("bot"):
                game["bots"].setdefault(i, {})["solve_at"] = t+random.uniform(BOT_SOLVE_MIN, BOT_SOLVE_MAX)
            p = game["players"][i]
            by = (p or {}).get("name", "") or f"P{i+1}"
            shout(nd["x"], nd["y"], by)
    # tags
    for h in hunters():
        hr = game["runners"][h]
        for d in divers():
            dr = game["runners"][d]
            if not dr["alive"]: continue
            if math.hypot(hr["x"]-dr["x"], hr["y"]-dr["y"]) < TAG_DIST:
                dr["alive"] = False
                release_slots(d)
                game["event_id"] += 1
                game["last_tag"] = {"hunter": h+1, "victim": d+1, "id": game["event_id"]}
    if divers() and not any(game["runners"][d]["alive"] for d in divers()):
        win_round(1, "all divers tagged")
        return
    if game["cracked"] >= game["target"]:
        win_round(2, "nodes cracked")
        return
    if t >= game["round_time_end"]:
        win_round(2, "survived the dark")
        return
    game["rings"] = [rg for rg in game["rings"] if t-rg["born"] < 2.0]
    game["blips"] = [b for b in game["blips"] if t < b["until"]]

def solve_attempt(seat, links):
    for ni, nd in enumerate(game["nodes"]):
        for si, sl in enumerate(nd["slots"]):
            if sl["solver"] == seat and not sl["resolved"]:
                try:
                    got = {(int(a), int(b)) for a, b in links}
                except (TypeError, ValueError):
                    return {"ok": False}
                want = {(s, d) for d, s in enumerate(sl["perm"])}
                if got == want and len(got) == sl["n"]:
                    sl["resolved"] = True; sl["solver"] = None
                    nd["done"] += 1
                    p = game["players"][seat]
                    by = (p or {}).get("name", "") or f"P{seat+1}"
                    shout(nd["x"], nd["y"], by)
                    if nd["done"] >= 3:
                        game["cracked"] += 1
                        game["event_id"] += 1
                        game["last_collect"] = {"by": by, "done": game["cracked"],
                                                "target": game["target"],
                                                "id": game["event_id"]}
                        game["nodes"].pop(ni)
                        if game["cracked"] < game["target"]:
                            spawn_node()
                    return {"ok": True}
                return {"ok": False}
    return {"ok": False}

def bot_perceive(seat):
    """Role-locked perception: only what that role's phone would show.
    Divers: nodes (global-dim) + hunters inside 280px fog. Never exact far positions.
    Wardens: sonar blips + divers inside 90px proximity + fellow hunters. Never nodes."""
    r = game["runners"][seat]
    t = now()
    if game["roles"][seat] == "diver":
        seen = []
        for h in hunters():
            hr = game["runners"][h]
            if math.hypot(hr["x"]-r["x"], hr["y"]-r["y"]) < 280:
                seen.append((hr["x"], hr["y"]))
        return {"hunters": seen,
                "nodes": [(nd["x"], nd["y"]) for nd in game["nodes"] if nd["done"] < 3]}
    seen_cu = []
    for d in divers():
        dr = game["runners"][d]
        if dr["alive"] and math.hypot(dr["x"]-r["x"], dr["y"]-r["y"]) < 90:
            seen_cu.append((dr["x"], dr["y"]))
    return {"blips": [(b["x"], b["y"]) for b in game["blips"] if t < b["until"]],
            "close": seen_cu,
            "mates": [h for h in hunters() if h != seat]}

def bot_diver_move(i, st):
    r = game["runners"][i]
    if holds_slot(i) or game["channel"][i]:
        return (0, 0)  # sit: keep channeling, stay silent
    px = bot_perceive(i)
    if px["hunters"]:
        hx, hy = min(px["hunters"], key=lambda h: math.hypot(h[0]-r["x"], h[1]-r["y"]))
        dx, dy = r["x"]-hx, r["y"]-hy
        n = math.hypot(dx, dy) or 1
        return (dx/n, dy/n)  # full sprint away (drains stamina like anyone)
    if px["nodes"]:
        nx, ny = min(px["nodes"], key=lambda q: math.hypot(q[0]-r["x"], q[1]-r["y"]))
        dx, dy = nx-r["x"], ny-r["y"]
        n = math.hypot(dx, dy) or 1
        if n < NODE_R*0.5:
            return (0, 0)  # parked on the node: silent channel
        return (dx/n, dy/n)
    return (0, 0)

def bot_hunter_move(i, st, t):
    r = game["runners"][i]
    px = bot_perceive(i)
    targets = list(px["blips"]) + px["close"]
    if targets:
        tx, ty = min(targets, key=lambda q: math.hypot(q[0]-r["x"], q[1]-r["y"]))
        dx, dy = tx-r["x"], ty-r["y"]
        n = math.hypot(dx, dy) or 1
        if n < TAG_DIST*0.5:
            return (0, 0)
        return (dx/n, dy/n)
    wp = st.get("wp")
    if not wp or math.hypot(wp[0]-r["x"], wp[1]-r["y"]) < 40 or t-st.get("wp_at", 0) > 6:
        for _ in range(30):
            x = 70+random.random()*(W-140); y = 70+random.random()*(H-140)
            if point_clear(x, y): break
        st["wp"] = (x, y); st["wp_at"] = t
        wp = st["wp"]
    dx, dy = wp[0]-r["x"], wp[1]-r["y"]
    n = math.hypot(dx, dy) or 1
    return (dx/n, dy/n)

def bot_try_solve(i):
    for nd in game["nodes"]:
        for sl in nd["slots"]:
            if sl["solver"] == i and not sl["resolved"]:
                solve_attempt(i, [[s, d] for d, s in enumerate(sl["perm"])])
                return

def bot_brain(dt):
    t = now()
    for i, p in enumerate(game["players"]):
        if not p or not p.get("bot"): continue
        p["last_seen"] = t
        role = game["roles"][i]
        if game["phase"] != "playing" or not role:
            p["input"] = {"x": 0, "y": 0}
            continue
        if role == "diver" and not game["runners"][i]["alive"]:
            p["input"] = {"x": 0, "y": 0}
            continue
        st = game["bots"].setdefault(i, {})
        if role == "diver":
            if holds_slot(i) and t >= st.get("solve_at", 0):
                bot_try_solve(i)
            mv = bot_diver_move(i, st)
        else:
            mv = bot_hunter_move(i, st, t)
        p["input"] = {"x": mv[0], "y": mv[1]}

def set_bots(n):
    humans = [i for i, p in enumerate(game["players"]) if p and not p.get("bot")]
    bots = [i for i, p in enumerate(game["players"]) if p and p.get("bot")]
    n = max(0, min(n, MAX_PLAYERS-len(humans)))
    while len(bots) > n:  # drop highest seats first
        i = bots.pop()
        release_slots(i)
        game["hunt_counts"].pop(game["players"][i]["id"], None)
        game["bots"].pop(i, None)
        game["players"][i] = None; game["roles"][i] = None
        game["channel"][i] = None
    used = {p["name"] for p in game["players"] if p}
    while len(bots) < n:
        i = next(i for i in range(MAX_PLAYERS) if not game["players"][i])
        name = next(nm for nm in BOT_NAMES if f"🤖 {nm}" not in used)
        pid = f"bot-{name}-{random.randrange(1000000)}"
        game["players"][i] = {"id": pid, "name": f"🤖 {name}", "last_seen": now(),
                              "ready": True, "input": {"x": 0, "y": 0}, "bot": True}
        game["bots"][i] = {}
        used.add(f"🤖 {name}")
        bots.append(i)
    return sum(1 for p in game["players"] if p and p.get("bot"))

def game_loop():
    last = time.time()
    while True:
        t = time.time(); dt = min(0.05, t-last); last = t
        with lock:
            free_stale()
            seated = [p for p in game["players"] if p]
            if game["phase"] == "waiting" and len(seated) >= 2 and all(p["ready"] for p in seated) and any(not p.get("bot") for p in seated):
                game["phase"] = "countdown"; game["countdown_end"] = now()+2.4
                reset_match(); assign_roles()
            step(dt)
        time.sleep(1/60)

def snapshot(pid):
    me = slot_of(pid)
    role = game["roles"][me] if me >= 0 else None
    t = now()
    if role == "hunter":
        nodes_out = []
    else:
        nodes_out = [{"x": round(nd["x"], 1), "y": round(nd["y"], 1), "done": nd["done"],
                      "busy": any(sl["solver"] is not None for sl in nd["slots"])}
                     for nd in game["nodes"]]
    myslot = None
    if me >= 0:
        for ni, nd in enumerate(game["nodes"]):
            for si, sl in enumerate(nd["slots"]):
                if sl["solver"] == me:
                    myslot = {"node": ni, "slot": si, "n": sl["n"],
                              "src": sl["src"], "dst": sl["dst"]}
    return {
        "v": VERSION,
        "phase": game["phase"],
        "countdown": max(1, math.ceil(game["countdown_end"]-t)) if game["phase"] == "countdown" else 0,
        "round": game["round"],
        "h_wins": game["h_wins"], "d_wins": game["d_wins"],
        "winner_side": game["winner_side"],
        "timeleft": round(max(0, game["round_time_end"]-t), 1) if game["phase"] == "playing" else 0,
        "you": {"slot": me+1 if me >= 0 else 0, "role": role or ""},
        "roles": game["roles"],
        "pillars": PILLARS,
        "runners": [{"x": round(game["runners"][i]["x"], 1), "y": round(game["runners"][i]["y"], 1),
                     "alive": game["runners"][i]["alive"],
                     "stam": round(game["runners"][i]["stam"], 2)} for i in range(MAX_PLAYERS)],
        "nodes": nodes_out,
        "myslot": myslot,
        "target": game["target"], "cracked": game["cracked"],
        "rings": [{"x": r["x"], "y": r["y"], "age": round(t-r["born"], 2)} for r in game["rings"]],
        "blips": [{"x": b["x"], "y": b["y"], "str": b["str"],
                   "left": round(b["until"]-t, 2)} for b in game["blips"] if t < b["until"]],
        "names": [((game["players"][i] or {}).get("name", "") or "") for i in range(MAX_PLAYERS)],
        "ready": [bool(game["players"][i] and game["players"][i]["ready"]) for i in range(MAX_PLAYERS)],
        "connected": [bool(game["players"][i]) for i in range(MAX_PLAYERS)],
        "last_ping": game["last_ping"],
        "last_shout": game["last_shout"],
        "last_tag": game["last_tag"],
        "last_collect": game["last_collect"],
        "last_round": game["last_round"],
        "event_id": game["event_id"],
        "host": game["ips"].get(pid) in ("127.0.0.1", "::1"),
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
                if pid: game["ips"][pid] = self.client_address[0]
                touch(pid)
                s=snapshot(pid)
            return self._json(s)
        return super().do_GET()
    def do_POST(self):
        if self.path.startswith("/api/"):
            d=self._body()
            pid=str(d.get("clientId",""))[:32]
            with lock:
                if pid: game["ips"][pid] = self.client_address[0]
                if self.path=="/api/join":
                    name=str(d.get("name","Player"))[:32] or "Player"
                    s=slot_of(pid)
                    if s<0:
                        free_stale()
                        s=next((i for i in range(MAX_PLAYERS) if not game["players"][i]), -1)
                        if s<0: return self._json({"you":0})
                        game["players"][s]={"id":pid,"name":name,"last_seen":now(),"ready":False,
                                            "input":{"x":0,"y":0}}
                    else:
                        game["players"][s]["name"]=name; game["players"][s]["last_seen"]=now()
                    return self._json({"you":s+1})
                touch(pid); s=slot_of(pid)
                if self.path=="/api/input" and s>=0:
                    try:
                        ix=max(-1,min(1,float(d.get("x",0)))); iy=max(-1,min(1,float(d.get("y",0))))
                    except: ix,iy=0,0
                    n=math.hypot(ix,iy)
                    if n>1: ix/=n; iy/=n
                    game["players"][s]["input"]={"x":ix,"y":iy}
                    return self._json({"ok":True})
                if self.path=="/api/ready" and s>=0:
                    game["players"][s]["ready"]=True
                    return self._json({"ok":True})
                if self.path=="/api/leave":
                    if s>=0:  # browser closed: free the seat now, don't wait out the 8s timeout
                        release_slots(s)
                        game["players"][s]=None; game["roles"][s]=None
                    return self._json({"ok":True})
                if self.path=="/api/node_done" and s>=0:
                    return self._json(solve_attempt(s, d.get("links", [])))
                if self.path=="/api/node_abandon" and s>=0:
                    release_slots(s)
                    return self._json({"ok":True})
                if self.path=="/api/bot":
                    if self.client_address[0] not in ("127.0.0.1", "::1"):
                        return self._json({"ok": False, "err": "host only"})
                    try: n=int(d.get("n", 0))
                    except: n=0
                    return self._json({"ok": True, "bots": set_bots(n)})
                if self.path=="/api/restart":
                    if game["phase"]=="over":
                        reset_match(); assign_roles()
                        for p in game["players"]:
                            if p and not p.get("bot"): p["ready"]=False
                        game["phase"]="waiting"; game["winner_side"]=0; game["round"]=1
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
    print(f"\n  ECHO HUNT running!\n  On this laptop:  http://localhost:{PORT}")
    for ip in lan_ips(): print(f"  Friend on same WiFi:  http://{ip}:{PORT}")
    print(f"\n  2-8 players, one phone each. Wardens hunt blind on sonar;\n  divers crack wire nodes. First side to {WIN_ROUNDS} rounds wins!\n")
    try: srv.serve_forever()
    except KeyboardInterrupt: print("\nbye!")
