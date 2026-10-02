#!/usr/bin/env python3
"""Neon Horde — LAN 2-4 player co-op survivors-like. Zero deps, stdlib only.
Run:  python3 server.py   (each hunter opens the printed LAN URL on their own screen)
INFINITE field: no walls, camera follows you, minimap + arrows find the pack.
Biomes by distance from spawn: meadow -> ember -> void (tougher, richer gems).
Endless horde, per-player level drafts, elites, a boss every 4 min, revives.
"""
import json, time, math, random, threading, socket, os
from http.server import ThreadingHTTPServer, SimpleHTTPRequestHandler
from pathlib import Path
import urllib.parse

PORT = int(os.environ.get("PORT", "3003"))
VERSION = "1.9"  # bump on every update; shown on the site
MAX_SEATS = 4
PUBLIC = Path(__file__).parent / "public"

VIEW_R = 1350       # snapshot interest radius per viewer
SPAWN_MIN, SPAWN_MAX = 950, 1250   # spawn ring around players
DESPAWN_R = 2200    # enemies past this from EVERYONE despawn
GEM_DESPAWN_R = 2600
ENEMY_CAP = 110
GEM_CAP = 170
BOSS_EVERY = 240
ELITE_EVERY = 45
TIER_DIST = 900     # biome ring width from origin

lock = threading.Lock()
game = {
    "phase": "waiting",  # waiting|countdown|playing|over
    "countdown_end": 0,
    "time": 0.0,
    "players": [None]*MAX_SEATS,
    "enemies": [],  # {x,y,hp,maxhp,spd,dmg,r,kind,elite,tier,boss,atk,blade_hit}
    "shots": [],
    "gems": [],
    "boss": None,
    "axes": [],   # {x,y,vx,vy,owner,dmg,dist,maxd,back,throw}
    "acid": [],   # {x,y,r,dps,owner,until,tick}
    "fx": [],     # transient visuals {kind,pts,until}
    "throw_n": 0,
    "spawn_t": 1.0,
    "elite_t": 30.0,
    "boss_t": BOSS_EVERY,
    "boss_n": 0,
    "blood_until": 0.0,
    "blood_next": 180.0,
    "kills": 0,
    "event_id": 0,
    "last_lvl": None,
    "last_boss": None,
    "last_moon": None,
    "last_goblin": None,
    "last_hurt": None,  # {seat,amt,fx,fy,id} incoming-damage indicator feed
    "last_over": None,
}

def now(): return time.time()
def xp_next(lv): return int(6 + lv*3 + lv*lv*0.2)
def tier_at(x, y): return min(2, int(math.hypot(x, y)/TIER_DIST))

def mkbuild():
    return {"hp": 100.0, "maxhp": 100.0, "level": 1, "xp": 0, "xpn": xp_next(1),
            "speed": 250.0, "dmg": 1.0, "cdr": 1.0, "armor": 0, "magnet": 95.0,
            "lifesteal": 0.0, "thorns": 0, "revive_mult": 1.0,
            "wpn": {}, "arti": [], "evo": [], "pending": None, "pend_t": 0,
            "down": False, "revive": 0.0, "prot": 0.0, "bleed": 0.0, "dead": False,
            "blade_a": 0.0, "frost_a": 0.0, "bolt_t": 0.0, "nova_t": 0.0,
            "chain_t": 0.0, "msl_t": 0.0, "axe_t": 0.0, "acid_t": 0.0, "kills": 0}

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
def occupied():
    return [i for i, p in enumerate(game["players"]) if p]

# ---------------- draft ----------------
STAT_OPTS = [
    ("s_speed", "Swift Boots", "+8% move speed", 3),
    ("s_hp", "Vitality", "+20 max HP, heal 20", 3),
    ("s_dmg", "Power Core", "+12% damage", 3),
    ("s_cdr", "Overclock", "weapons fire 8% faster", 3),
    ("s_mag", "Magnet Coil", "+35% pickup radius", 2),
    ("s_armor", "Plating", "+1 armor (flat)", 2),
]
ARTI_OPTS = [
    ("a_life", "Lifesteal Fangs", "heal 4% of damage dealt", 2),
    ("a_thorn", "Thorn Mail", "attackers take 6", 2),
    ("a_medic", "Field Medic", "revive 40% faster, revives heal you 25", 2),
    ("a_wisdom", "Wisdom Charm", "+20% XP gained", 2),
]
WPN_NAMES = {"w_blades": "Orbit Blades", "w_bolts": "Bolt Spitter", "w_nova": "Nova Pulse",
             "w_chain": "Chain Lightning", "w_frost": "Frost Orbitals", "w_msl": "Homing Missiles",
             "w_axe": "Boomerang Axe", "w_acid": "Acid Pools"}
WPN_ALL = ("w_blades", "w_bolts", "w_nova", "w_chain", "w_frost", "w_msl", "w_axe", "w_acid")
EVO = {
    "w_blades": ("a_life", "e_reaper", "Soul Reaper", "blades execute under 15% HP"),
    "w_bolts": ("a_wisdom", "e_oracle", "Oracle Barrage", "bolts seek targets"),
    "w_nova": ("a_thorn", "e_thornova", "Thorn Nova", "huge, slows 2s"),
}

def draft_options(b):
    pool = []
    for w in WPN_ALL:
        if w in EVO:
            art, eid, enm, eds = EVO[w]
            if b["wpn"].get(w, 0) >= 5 and art in b["arti"] and eid not in b["evo"]:
                pool.append((eid, enm, eds+" (EVOLVE)", 5))
                continue
        if w not in b["wpn"]: pool.append((w, WPN_NAMES[w] + " NEW", "a new auto-weapon", 3))
        elif b["wpn"][w] < 5: pool.append((w, WPN_NAMES[w]+" +"+str(b["wpn"][w]+1), "upgrade to Lv"+str(b["wpn"][w]+1), 3))
    for oid, nm, ds, w in STAT_OPTS:
        if oid == "s_cdr" and b["cdr"] <= 0.55: continue
        pool.append((oid, nm, ds, w))
    for oid, nm, ds, w in ARTI_OPTS:
        if oid not in b["arti"]: pool.append((oid, nm, ds, w))
    if not pool:
        return [{"id": "s_heal", "name": "Repair", "desc": "heal 40"}]
    picks, bag = [], [p for p in pool]
    for _ in range(min(3, len(bag))):
        tot = sum(p[3] for p in bag)
        r = random.random()*tot; acc = 0
        for p in bag:
            acc += p[3]
            if r <= acc:
                picks.append({"id": p[0], "name": p[1], "desc": p[2]})
                bag.remove(p); break
    if picks and not any(o["id"].startswith(("w_", "e_")) for o in picks):
        wpool = [p for p in pool if p[0].startswith(("w_", "e_"))]
        if wpool:
            w = random.choice(wpool)
            picks[-1] = {"id": w[0], "name": w[1], "desc": w[2]}
    # evolutions are build-defining: always surface an eligible one
    elig = [p for p in pool if p[0].startswith("e_") and p[0] not in [o["id"] for o in picks]]
    if elig and not any(o["id"].startswith("e_") for o in picks):
        w = random.choice(elig)
        picks[-1] = {"id": w[0], "name": w[1], "desc": w[2]}
    return picks

def apply_pick(i, oid):
    b = game["players"][i]
    if oid == "s_heal" or (not oid):
        b["hp"] = min(b["maxhp"], b["hp"]+40); return
    if oid in WPN_NAMES:
        b["wpn"][oid] = b["wpn"].get(oid, 0)+1; return
    for w, (art, eid, _, _) in EVO.items():
        if oid == eid:
            b["evo"].append(eid); return
    if oid == "s_speed": b["speed"] *= 1.08
    elif oid == "s_hp": b["maxhp"] += 20; b["hp"] = min(b["maxhp"], b["hp"]+20)
    elif oid == "s_dmg": b["dmg"] *= 1.12
    elif oid == "s_cdr": b["cdr"] = max(0.55, b["cdr"]*0.92)
    elif oid == "s_mag": b["magnet"] *= 1.35
    elif oid == "s_armor": b["armor"] += 1
    elif oid == "a_life": b["lifesteal"] = 0.04; b["arti"].append(oid)
    elif oid == "a_thorn": b["thorns"] = 6; b["arti"].append(oid)
    elif oid == "a_medic": b["revive_mult"] = 1.4; b["arti"].append(oid)
    elif oid == "a_wisdom": b["arti"].append(oid)

def emit_fx(kind, pts):
    game["fx"].append({"kind": kind, "pts": pts, "until": now()+0.45})
    if len(game["fx"]) > 24:
        del game["fx"][0]

def nearest_enemy(x, y, maxd=1e9, skip=None):
    best, bd = None, maxd
    for e in game["enemies"]:
        if e.get("dead") or e is skip: continue
        d = math.hypot(e["x"]-x, e["y"]-y)
        if d < bd: best, bd = e, d
    return best

def gain_xp(i, amt):
    b = game["players"][i]
    if "a_wisdom" in b["arti"]: amt = int(amt*1.2)
    if now() < game["blood_until"]: amt *= 2
    b["xp"] += amt
    while b["xp"] >= b["xpn"] and not b["pending"]:
        b["xp"] -= b["xpn"]; b["level"] += 1; b["xpn"] = xp_next(b["level"])
        b["pending"] = draft_options(b); b["pend_t"] = now()+30
        game["event_id"] += 1
        game["last_lvl"] = {"seat": i+1, "lvl": b["level"], "id": game["event_id"]}

# ---------------- enemies ----------------
def hp_scale(): return 1+game["time"]/60*0.30
def dmg_scale(): return 1+game["time"]/60*0.12

def spawn_enemy(elite=False, boss=False):
    occ = occupied()
    if boss:
        anchor = game["players"][random.choice(occ)] if occ else {"x": 0, "y": 0}
        a = random.random()*2*math.pi
        x, y = anchor["x"]+math.cos(a)*1100, anchor["y"]+math.sin(a)*1100
        n = game["boss_n"]
        hp = 1500*(1+n*0.9)*hp_scale()
        e = {"x": x, "y": y, "hp": hp, "maxhp": hp, "spd": 72, "dmg": 26*dmg_scale(),
             "r": 34, "kind": "boss", "elite": False, "tier": 1, "boss": True, "atk": 0, "kx": 0.0, "ky": 0.0, "blade_hit": 0}
        game["boss"] = e; return e
    anchor = game["players"][random.choice(occ)] if occ else {"x": 0, "y": 0}
    a = random.random()*2*math.pi
    d = SPAWN_MIN+random.random()*(SPAWN_MAX-SPAWN_MIN)
    x, y = anchor["x"]+math.cos(a)*d, anchor["y"]+math.sin(a)*d
    tier = tier_at(x, y)
    t = game["time"]
    r = random.random()
    if r > 0.90:
        kind = "brute"
    elif r > 0.82 and t > 90:
        kind = "charger"
    elif r > 0.74 and t > 60:
        kind = "splitter"
    elif r > 0.62:
        kind = "darter"
    else:
        kind = "chaser"
    base = {"chaser": (22, 96, 8, 13), "darter": (12, 152, 6, 10), "brute": (72, 58, 14, 19),
            "splitter": (30, 82, 8, 14), "charger": (46, 66, 12, 15)}[kind]
    hp, spd, dmg, rr = base
    hp *= (1+tier*0.6); dmg *= (1+tier*0.3)
    if elite: hp *= 7; spd *= 1.25; dmg *= 1.5; rr += 4
    return {"x": x, "y": y, "hp": hp*hp_scale(), "maxhp": hp*hp_scale(), "spd": spd,
            "dmg": dmg*dmg_scale(), "r": rr, "kind": kind, "elite": elite,
            "tier": tier, "boss": False, "atk": 0, "kx": 0.0, "ky": 0.0, "blade_hit": 0,
            "mode": "chase", "mode_t": 0, "dx": 0, "dy": 0, "slow_until": 0,
            "despawn": 0}

def spawn_goblin():
    occ = occupied()
    anchor = game["players"][random.choice(occ)] if occ else {"x": 0, "y": 0}
    a = random.random()*2*math.pi
    x, y = anchor["x"]+math.cos(a)*1100, anchor["y"]+math.sin(a)*1100
    hp = 40*hp_scale()
    return {"x": x, "y": y, "hp": hp, "maxhp": hp, "spd": 205, "dmg": 0,
            "r": 12, "kind": "goblin", "elite": False, "tier": 1, "boss": False,
            "atk": 0, "kx": 0.0, "ky": 0.0, "blade_hit": 0, "mode": "flee", "mode_t": 0, "dx": 0, "dy": 0,
            "slow_until": 0, "despawn": now()+20}

def gem_value(tier, elite):
    return (3 if elite else 1)+tier

def seg_circle(x1, y1, x2, y2, cx, cy, r):
    # explicit swept collision: does segment p1->p2 touch circle (c, r)?
    dx, dy = x2-x1, y2-y1
    l2 = dx*dx+dy*dy
    t = 0.0 if l2 == 0 else max(0.0, min(1.0, ((cx-x1)*dx+(cy-y1)*dy)/l2))
    px, py = x1+dx*t, y1+dy*t
    return (cx-px)*(cx-px)+(cy-py)*(cy-py) < r*r

def hurt_enemy(e, dmg, owner):
    b = game["players"][owner]
    if not e.get("boss") and not e.get("elite") and "e_reaper" in b.get("evo", []):
        if e["hp"]/e["maxhp"] < 0.15:
            dmg = max(dmg, e["maxhp"])  # Soul Reaper executes the weak
    e["hp"] -= dmg
    if e["hp"] <= 0 and not e.get("dead"):
        e["dead"] = True
        game["kills"] += 1
        if e["kind"] == "splitter" and len(game["enemies"]) < ENEMY_CAP+10:
            for sgn in (-1, 1):
                hp = 14*hp_scale()
                game["enemies"].append({"x": e["x"]+sgn*16, "y": e["y"], "hp": hp, "maxhp": hp,
                    "spd": 110, "dmg": 6*dmg_scale(), "r": 10, "kind": "chaser",
                    "elite": False, "tier": e["tier"], "boss": False, "atk": 0, "kx": 0.0, "ky": 0.0,
                    "blade_hit": 0, "mode": "chase", "mode_t": 0, "dx": 0, "dy": 0,
                    "slow_until": 0, "despawn": 0})
        b = game["players"][owner]
        b["kills"] += 1
        if b["lifesteal"]:
            b["hp"] = min(b["maxhp"], b["hp"]+dmg*b["lifesteal"])
        nv = 8 if e["elite"] else 1
        for _ in range(nv):
            game["gems"].append({"x": e["x"]+random.uniform(-14, 14),
                                 "y": e["y"]+random.uniform(-14, 14),
                                 "v": gem_value(e["tier"], e["elite"])})
        if e.get("boss"):
            game["boss"] = None
            for _ in range(20):
                game["gems"].append({"x": e["x"]+random.uniform(-30, 30),
                                     "y": e["y"]+random.uniform(-30, 30), "v": 3})
            for i in occupied():
                pb = game["players"][i]
                pb["hp"] = min(pb["maxhp"], pb["hp"]+30)

def hurt_player(i, dmg):
    b = game["players"][i]
    if b["down"] or b["pending"] or now() < b["prot"]: return
    dmg = max(1, dmg-b["armor"])
    b["hp"] -= dmg
    if b["hp"] <= 0:
        b["hp"] = 0; b["down"] = True; b["revive"] = 0; b["bleed"] = 25.0
        b["pending"] = None

def near_any(x, y, r):
    return any(math.hypot(x-game["players"][i]["x"], y-game["players"][i]["y"]) < r
               for i in occupied())

def step(dt):
    if game["phase"] == "countdown" and now() >= game["countdown_end"]:
        game["phase"] = "playing"
        return
    if game["phase"] != "playing": return
    t = now()
    game["time"] += dt
    blood = t < game["blood_until"]
    occ = occupied()
    # --- players move (frozen while drafting or down); no walls, infinite field ---
    for i in occ:
        b = game["players"][i]
        if b["down"] or b["pending"]: continue
        ix = max(-1, min(1, b["input"]["x"])); iy = max(-1, min(1, b["input"]["y"]))
        n = math.hypot(ix, iy)
        if n > 1: ix /= n; iy /= n
        b["x"] += ix*b["speed"]*dt
        b["y"] += iy*b["speed"]*dt
    # --- revives (downed bleed out in 25s) ---
    downs = [i for i in occ if game["players"][i]["down"] and not game["players"][i]["dead"]]
    ups = [i for i in occ if not game["players"][i]["down"]]
    for i in downs:
        b = game["players"][i]
        b["bleed"] -= dt
        if b["bleed"] <= 0:
            b["dead"] = True  # bled out: spectate the rest
            continue
        near = any(math.hypot(b["x"]-game["players"][j]["x"], b["y"]-game["players"][j]["y"]) < 60 for j in ups)
        if near:
            b["revive"] += dt*game["players"][i].get("revive_mult", 1)/3.0
            if b["revive"] >= 1:
                b["down"] = False; b["revive"] = 0
                b["hp"] = b["maxhp"]*0.5; b["prot"] = now()+2
                for j in ups:
                    if math.hypot(b["x"]-game["players"][j]["x"], b["y"]-game["players"][j]["y"]) < 60:
                        if "a_medic" in game["players"][j]["arti"]:
                            hj = game["players"][j]
                            hj["hp"] = min(hj["maxhp"], hj["hp"]+25)
    if occ and all(game["players"][i]["down"] for i in occ):
        game["phase"] = "over"
        game["event_id"] += 1
        game["last_over"] = {"time": round(game["time"], 1), "kills": game["kills"], "id": game["event_id"]}
        return
    # --- spawning around the pack (blood moon doubles the rate) ---
    interval = max(0.22, 1.1-game["time"]*0.0016)*(0.5 if blood else 1.0)
    game["spawn_t"] -= dt
    if game["spawn_t"] <= 0 and len(game["enemies"]) < ENEMY_CAP:
        game["spawn_t"] = interval
        batch = 1+int(game["time"]/75)+max(0, len(occ)-1)
        for _ in range(batch):
            if len(game["enemies"]) < ENEMY_CAP:
                game["enemies"].append(spawn_enemy())
    game["elite_t"] -= dt
    if game["elite_t"] <= 0:
        game["elite_t"] = ELITE_EVERY
        if len(game["enemies"]) < ENEMY_CAP:
            game["enemies"].append(spawn_enemy(elite=True))
    game["boss_t"] -= dt
    if game["boss_t"] <= 0:
        game["boss_t"] = BOSS_EVERY
        game["boss_n"] += 1
        game["enemies"].append(spawn_enemy(boss=True))
        game["event_id"] += 1
        game["last_boss"] = {"n": game["boss_n"], "id": game["event_id"]}
    # blood moon: 30s of double spawns + double XP every 3 min (game-time trigger!)
    if game["time"] >= game["blood_next"]:
        game["blood_next"] += 180.0
        game["blood_until"] = t+30.0
        game["event_id"] += 1
        game["last_moon"] = {"id": game["event_id"]}
    # greed goblin wanders in every ~50s
    if random.random() < dt/50 and sum(1 for e in game["enemies"] if e["kind"] == "goblin") < 1:
        if len(game["enemies"]) < ENEMY_CAP:
            game["enemies"].append(spawn_goblin())
            game["event_id"] += 1
            game["last_goblin"] = {"id": game["event_id"]}
    # despawn what nobody can see anymore (boss persists)
    game["enemies"] = [e for e in game["enemies"]
                       if e.get("boss") or e.get("dead") or near_any(e["x"], e["y"], DESPAWN_R)]
    game["gems"] = [gm for gm in game["gems"] if near_any(gm["x"], gm["y"], GEM_DESPAWN_R)]
    # --- weapons (unchanged systems) ---
    for i in occ:
        b = game["players"][i]
        if b["down"] or b["pending"]: continue
        lv = b["wpn"].get("w_blades", 0)
        if lv:
            rate = 2.6+0.3*lv
            a0 = b["blade_a"]
            b["blade_a"] += dt*rate
            n = lv+1; rr = 95+10*lv
            for k in range(n):
                pa = a0+k*2*math.pi/n
                ca = b["blade_a"]+k*2*math.pi/n
                x1, y1 = b["x"]+math.cos(pa)*rr, b["y"]+math.sin(pa)*rr
                x2, y2 = b["x"]+math.cos(ca)*rr, b["y"]+math.sin(ca)*rr
                for e in game["enemies"]:
                    if e.get("dead") or now() < e["blade_hit"]: continue
                    if seg_circle(x1, y1, x2, y2, e["x"], e["y"], e["r"]+10):
                        e["blade_hit"] = now()+0.35
                        hurt_enemy(e, (8+4*lv)*b["dmg"], i)
        lv = b["wpn"].get("w_bolts", 0)
        if lv:
            b["bolt_t"] -= dt
            if b["bolt_t"] <= 0 and game["enemies"]:
                b["bolt_t"] = 1.1*b["cdr"]
                tgt = min((e for e in game["enemies"] if not e.get("dead")),
                          key=lambda e: math.hypot(e["x"]-b["x"], e["y"]-b["y"]), default=None)
                if tgt:
                    home = "e_oracle" in b.get("evo", [])
                    for k in range(lv):
                        a = math.atan2(tgt["y"]-b["y"], tgt["x"]-b["x"])+(k-(lv-1)/2)*0.12
                        game["shots"].append({"x": b["x"], "y": b["y"],
                            "vx": math.cos(a)*700, "vy": math.sin(a)*700,
                            "dmg": (12+6*lv)*b["dmg"], "owner": i, "life": 1.6,
                            "home": home})
        lv = b["wpn"].get("w_nova", 0)
        if lv:
            b["nova_t"] -= dt
            if b["nova_t"] <= 0:
                b["nova_t"] = (4.5-0.3*lv)*b["cdr"]
                thorn = "e_thornova" in b.get("evo", [])
                rr = (130+15*lv)*(1.8 if thorn else 1)
                dmg = (20+10*lv)*b["dmg"]*(1.8 if thorn else 1)
                emit_fx("thornova" if thorn else "nova",
                        [[round(b["x"], 1), round(b["y"], 1), round(rr, 1)]])
                for e in game["enemies"]:
                    if e.get("dead"): continue
                    d = math.hypot(e["x"]-b["x"], e["y"]-b["y"])
                    if d < rr+e["r"]:
                        hurt_enemy(e, dmg, i)
                        if thorn: e["slow_until"] = now()+2.0
                        if d > 1:
                            e["kx"] = e.get("kx", 0)+(e["x"]-b["x"])/d*520
                            e["ky"] = e.get("ky", 0)+(e["y"]-b["y"])/d*520
        # frost orbitals: slow company that chills on touch
        lv = b["wpn"].get("w_frost", 0)
        if lv:
            b["frost_a"] += dt*1.7
            n = 1+lv//2
            for k in range(n):
                a = b["frost_a"]+k*2*math.pi/n
                x1, y1 = b["x"]+math.cos(a)*120, b["y"]+math.sin(a)*120
                for e in game["enemies"]:
                    if e.get("dead") or now() < e.get("frost_hit", 0): continue
                    if math.hypot(x1-e["x"], y1-e["y"]) < e["r"]+10:
                        e["frost_hit"] = now()+0.4
                        e["slow_until"] = now()+1.5
                        hurt_enemy(e, (6+3*lv)*b["dmg"], i)
        # chain lightning: near strike that jumps while it can
        lv = b["wpn"].get("w_chain", 0)
        if lv:
            b["chain_t"] -= dt
            if b["chain_t"] <= 0:
                tgt = nearest_enemy(b["x"], b["y"], 520)
                if tgt:
                    b["chain_t"] = (2.4-0.2*lv)*b["cdr"]
                    pts = [[round(b["x"], 1), round(b["y"], 1)]]
                    dmg = (16+9*lv)*b["dmg"]
                    seen = set()
                    cur = tgt
                    for _ in range(2+lv):
                        if cur is None or id(cur) in seen: break
                        seen.add(id(cur))
                        pts.append([round(cur["x"], 1), round(cur["y"], 1)])
                        hurt_enemy(cur, dmg, i)
                        dmg *= 0.78
                        nxt, bd = None, 230
                        for e in game["enemies"]:
                            if e.get("dead") or id(e) in seen: continue
                            d = math.hypot(e["x"]-cur["x"], e["y"]-cur["y"])
                            if d < bd: nxt, bd = e, d
                        cur = nxt
                    emit_fx("chain", pts)
        # homing missiles: slow volley, small pop on arrival
        lv = b["wpn"].get("w_msl", 0)
        if lv:
            b["msl_t"] -= dt
            if b["msl_t"] <= 0 and game["enemies"]:
                b["msl_t"] = (2.8-0.25*lv)*b["cdr"]
                for k in range(min(lv, 5)):
                    a = random.random()*2*math.pi
                    game["shots"].append({"x": b["x"], "y": b["y"],
                        "vx": math.cos(a)*380, "vy": math.sin(a)*380,
                        "dmg": (14+8*lv)*b["dmg"], "owner": i, "life": 2.5,
                        "home": True, "aoe": 46})
        # boomerang axe: out 340px, then home; hits both ways, once per throw
        lv = b["wpn"].get("w_axe", 0)
        if lv:
            b["axe_t"] -= dt
            if b["axe_t"] <= 0:
                tgt = nearest_enemy(b["x"], b["y"], 640)
                if tgt:
                    b["axe_t"] = (3.0-0.25*lv)*b["cdr"]
                    a = math.atan2(tgt["y"]-b["y"], tgt["x"]-b["x"])
                    game["throw_n"] += 1
                    game["axes"].append({"x": b["x"], "y": b["y"],
                        "vx": math.cos(a)*520, "vy": math.sin(a)*520,
                        "owner": i, "dmg": (22+10*lv)*b["dmg"],
                        "dist": 0, "maxd": 340, "back": False, "throw": game["throw_n"]})
        # acid pools: melt the densest pack in reach
        lv = b["wpn"].get("w_acid", 0)
        if lv:
            b["acid_t"] -= dt
            if b["acid_t"] <= 0:
                bestc, bestn = None, 1
                for e in game["enemies"]:
                    if e.get("dead"): continue
                    if math.hypot(e["x"]-b["x"], e["y"]-b["y"]) > 600: continue
                    n = sum(1 for o in game["enemies"]
                            if not o.get("dead") and math.hypot(o["x"]-e["x"], o["y"]-e["y"]) < 110)
                    if n > bestn: bestc, bestn = e, n
                if bestc:
                    b["acid_t"] = (5.0-0.4*lv)*b["cdr"]
                    game["acid"].append({"x": bestc["x"], "y": bestc["y"],
                        "r": 70+8*lv, "dps": (10+6*lv)*b["dmg"],
                        "owner": i, "until": now()+4.0, "tick": 0.0})
    # --- shots fly (swept: no tunneling; homing shells + missiles steer) ---
    for s in game["shots"]:
        ox, oy = s["x"], s["y"]
        if s.get("home"):
            sp = math.hypot(s["vx"], s["vy"]) or 1
            tgt = nearest_enemy(s["x"], s["y"], 700)
            if tgt:
                cur = math.atan2(s["vy"], s["vx"])
                want = math.atan2(tgt["y"]-s["y"], tgt["x"]-s["x"])
                dd = (want-cur+math.pi) % (2*math.pi) - math.pi
                na = cur + max(-3.2*dt, min(3.2*dt, dd))
                s["vx"], s["vy"] = math.cos(na)*sp, math.sin(na)*sp
        s["x"] += s["vx"]*dt; s["y"] += s["vy"]*dt; s["life"] -= dt
        if s["life"] <= 0:
            s["dead"] = True; continue
        for e in game["enemies"]:
            if e.get("dead"): continue
            if seg_circle(ox, oy, s["x"], s["y"], e["x"], e["y"], e["r"]+6):
                s["dead"] = True
                hurt_enemy(e, s["dmg"], s["owner"])
                if s.get("aoe"):  # missile splash
                    for o in game["enemies"]:
                        if o.get("dead") or o is e: continue
                        if math.hypot(o["x"]-e["x"], o["y"]-e["y"]) < s["aoe"]:
                            hurt_enemy(o, s["dmg"]*0.5, s["owner"])
                    emit_fx("pop", [[round(e["x"], 1), round(e["y"], 1)]])
                break
    game["shots"] = [s for s in game["shots"] if not s.get("dead")]
    # --- axes fly out, then home ---
    for ax in game["axes"]:
        ox, oy = ax["x"], ax["y"]
        if not ax["back"]:
            ax["x"] += ax["vx"]*dt; ax["y"] += ax["vy"]*dt
            ax["dist"] += 520*dt
            if ax["dist"] >= ax["maxd"]:
                ax["back"] = True
        else:
            o = game["players"][ax["owner"]]
            d = math.hypot(o["x"]-ax["x"], o["y"]-ax["y"]) or 1
            ax["vx"], ax["vy"] = (o["x"]-ax["x"])/d*560, (o["y"]-ax["y"])/d*560
            ax["x"] += ax["vx"]*dt; ax["y"] += ax["vy"]*dt
            if d < 34:
                ax["dead"] = True; continue
        for e in game["enemies"]:
            if e.get("dead") or e.get("axe_hit") == ax["throw"]: continue
            if seg_circle(ox, oy, ax["x"], ax["y"], e["x"], e["y"], e["r"]+12):
                e["axe_hit"] = ax["throw"]
                hurt_enemy(e, ax["dmg"], ax["owner"])
    game["axes"] = [ax for ax in game["axes"] if not ax.get("dead")]
    # --- acid pools melt + expire ---
    for ap in game["acid"]:
        ap["tick"] -= dt
        if ap["tick"] <= 0:
            ap["tick"] = 0.5
            for e in game["enemies"]:
                if e.get("dead"): continue
                if math.hypot(e["x"]-ap["x"], e["y"]-ap["y"]) < ap["r"]:
                    hurt_enemy(e, ap["dps"]*0.5, ap["owner"])
    game["acid"] = [ap for ap in game["acid"] if now() < ap["until"]]
    game["fx"] = [f for f in game["fx"] if now() < f["until"]]
    # --- enemies chase + chew (drafting players are ignored; horde idles if nobody is free) ---
    free = [i for i in occ if not game["players"][i]["down"] and not game["players"][i]["pending"]]
    for e in game["enemies"]:
        if e.get("dead"): continue
        if e.get("kx") or e.get("ky"):  # eased knockback glide (no more teleport pops)
            e["x"] += e["kx"]*dt; e["y"] += e["ky"]*dt
            damp = max(0.0, 1.0-min(1.0, dt*7))
            e["kx"] *= damp; e["ky"] *= damp
        if not free: continue
        tgt = min(free, key=lambda i: math.hypot(e["x"]-game["players"][i]["x"], e["y"]-game["players"][i]["y"]))
        b = game["players"][tgt]
        d = math.hypot(b["x"]-e["x"], b["y"]-e["y"]) or 1
        spd = e["spd"]*(0.6 if t < e.get("slow_until", 0) else 1)
        if e["kind"] == "goblin":
            if t > e["despawn"]:
                e["dead"] = True; continue
            e["x"] -= (b["x"]-e["x"])/d*spd*dt
            e["y"] -= (b["y"]-e["y"])/d*spd*dt
            continue
        dmg = e["dmg"]
        if e["kind"] == "charger":
            md = e.get("mode", "chase")
            if md == "chase":
                e["x"] += (b["x"]-e["x"])/d*spd*dt
                e["y"] += (b["y"]-e["y"])/d*spd*dt
                if d < 380 and t >= e.get("rest_until", 0):
                    e["mode"] = "wind"; e["mode_t"] = 0.7
                    e["dx"], e["dy"] = (b["x"]-e["x"])/d, (b["y"]-e["y"])/d
            elif md == "wind":
                e["mode_t"] -= dt
                if e["mode_t"] <= 0:
                    e["mode"] = "dash"; e["mode_t"] = 0.5
            elif md == "dash":
                e["x"] += e["dx"]*520*dt; e["y"] += e["dy"]*520*dt
                dmg = e["dmg"]*1.5
                e["mode_t"] -= dt
                if e["mode_t"] <= 0:
                    e["mode"] = "chase"; e["rest_until"] = t+2.0
        else:
            e["x"] += (b["x"]-e["x"])/d*spd*dt
            e["y"] += (b["y"]-e["y"])/d*spd*dt
        if d < e["r"]+18 and t >= e["atk"]:
            e["atk"] = t+0.8
            hp0 = b["hp"]
            hurt_player(tgt, dmg)
            if b["hp"] < hp0:
                game["event_id"] += 1
                game["last_hurt"] = {"seat": tgt+1, "amt": round(hp0-b["hp"], 1),
                                     "fx": round(e["x"], 1), "fy": round(e["y"], 1),
                                     "id": game["event_id"]}
            if b["thorns"]:
                hurt_enemy(e, b["thorns"], tgt)
    game["enemies"] = [e for e in game["enemies"] if not e.get("dead")]
    # --- gems drift + merge + magnet ---
    if len(game["gems"]) > GEM_CAP:
        game["gems"].sort(key=lambda g: g["v"])
        del game["gems"][:(len(game["gems"])-GEM_CAP)]
    for gm in game["gems"]:
        best, bd = -1, 1e9
        for i in occ:
            b = game["players"][i]
            if b["down"]: continue
            d = math.hypot(gm["x"]-b["x"], gm["y"]-b["y"])
            if d < bd: best, bd = i, d
        if best < 0: continue
        b = game["players"][best]
        if bd < b["magnet"]:
            gm["x"] += (b["x"]-gm["x"])/bd*420*dt if bd > 1 else 0
            gm["y"] += (b["y"]-gm["y"])/bd*420*dt if bd > 1 else 0
        if bd < 24:
            gm["dead"] = True
            gain_xp(best, gm["v"])
    game["gems"] = [gm for gm in game["gems"] if not gm.get("dead")]
    # draft timeouts auto-pick
    for i in occ:
        b = game["players"][i]
        if b["pending"] and now() >= b["pend_t"]:
            apply_pick(i, b["pending"][0]["id"])
            b["pending"] = None

def game_loop():
    last = time.time()
    while True:
        t = time.time(); dt = min(0.05, t-last); last = t
        with lock:
            free_stale()
            occ = occupied()
            if game["phase"] == "waiting" and occ and all(game["players"][i]["ready"] for i in occ):
                game["phase"] = "countdown"; game["countdown_end"] = now()+2.4
                game["time"] = 0; game["enemies"] = []; game["shots"] = []
                game["gems"] = []; game["boss"] = None; game["kills"] = 0
                game["axes"] = []; game["acid"] = []; game["fx"] = []
                game["spawn_t"] = 1.0; game["elite_t"] = 30.0
                game["boss_t"] = BOSS_EVERY; game["boss_n"] = 0
                game["blood_until"] = 0.0; game["blood_next"] = 180.0
                for i in occ:
                    b = game["players"][i]
                    nb = mkbuild()
                    nb["x"], nb["y"] = (i%2)*400-200, (i//2)*400-200
                    nb["prot"] = now()+3
                    game["players"][i].update(nb)
                    b["pending"] = draft_options(b); b["pend_t"] = now()+30
                    game["event_id"] += 1
                    game["last_lvl"] = {"seat": i+1, "lvl": 1, "id": game["event_id"]}
            step(dt)
        time.sleep(1/60)

def snap_viewer(pid):
    s = slot_of(pid)
    if s >= 0 and game["players"][s]:
        return s
    occ = occupied()
    return occ[0] if occ else -1

def snapshot(viewer=0):
    cd = 0
    if game["phase"] == "countdown": cd = max(1, math.ceil(game["countdown_end"]-now()))
    v = game["players"][viewer] if 0 <= viewer < MAX_SEATS and game["players"][viewer] else None
    vx, vy = (v["x"], v["y"]) if v else (0, 0)
    def close(x, y, r=VIEW_R):
        return (x-vx)*(x-vx)+(y-vy)*(y-vy) < r*r
    pls = []
    for i in range(MAX_SEATS):
        p = game["players"][i]
        pls.append({
            "x": round(p["x"], 1) if p else 0, "y": round(p["y"], 1) if p else 0,
            "hp": round(p["hp"], 1) if p else 0, "maxhp": p["maxhp"] if p else 0,
            "level": p["level"] if p else 0, "xp": p["xp"] if p else 0, "xpn": p["xpn"] if p else 0,
            "down": bool(p and p["down"]), "rev": round(p["revive"], 2) if p else 0,
            "bleed": round(p["bleed"], 1) if p else 0, "dead": bool(p and p.get("dead")),
            "wpn": dict(p["wpn"]) if p else {}, "arti": list(p["arti"]) if p else [],
            "evo": list(p["evo"]) if p else [],
            "blade": round(p["blade_a"], 3) if p else 0,
            "frost": round(p["frost_a"], 3) if p else 0,
            "pending": ([dict(o) for o in p["pending"]] if p and p["pending"] else None),
        })
    return {
        "v": VERSION,
        "phase": game["phase"],
        "countdown": cd,
        "time": round(game["time"], 1),
        "kills": game["kills"],
        "players": pls,
        "enemies": [[round(e["x"],1), round(e["y"],1), round(max(0, e["hp"]/e["maxhp"]),2),
                     {"chaser": 0, "darter": 1, "brute": 2, "boss": 3, "splitter": 4, "charger": 5, "goblin": 6}[e["kind"]],
                     1 if e["elite"] else 0, e["r"], e["tier"],
                     {"chase": 0, "wind": 1, "dash": 2, "flee": 3}.get(e.get("mode", "chase"), 0)]
                    for e in game["enemies"] if close(e["x"], e["y"])],
        "shots": [[round(s["x"],1), round(s["y"],1), round(s["vx"],1), round(s["vy"],1),
                   1 if s.get("aoe") else 0] for s in game["shots"] if close(s["x"], s["y"])],
        "axes": [[round(a["x"],1), round(a["y"],1), round(a["vx"],1), round(a["vy"],1)] for a in game["axes"]],
        "acid": [[round(a["x"],1), round(a["y"],1), round(a["r"],1),
                  round(max(0, a["until"]-now()),1)] for a in game["acid"]],
        "fx": [{"kind": f["kind"], "pts": f["pts"]} for f in game["fx"]],
        "gems": [[round(g["x"],1), round(g["y"],1), g["v"]] for g in game["gems"] if close(g["x"], g["y"])],
        "boss": ([round(game["boss"]["x"],1), round(game["boss"]["y"],1),
                  round(max(0, game["boss"]["hp"]/game["boss"]["maxhp"]),3),
                  round(max(0, game["boss"]["hp"]),1), round(game["boss"]["maxhp"],1)]
                 if game["boss"] else None),
        "names": [(game["players"][i] or {}).get("name","") or "" for i in range(MAX_SEATS)],
        "ready": [bool(game["players"][i] and game["players"][i]["ready"]) for i in range(MAX_SEATS)],
        "connected": [bool(game["players"][i]) for i in range(MAX_SEATS)],
        "last_lvl": game["last_lvl"],
        "last_moon": game["last_moon"],
        "blood": round(max(0, game["blood_until"]-now()), 1),
        "last_hurt": game["last_hurt"],
        "last_goblin": game["last_goblin"],
        "last_boss": game["last_boss"],
        "last_over": game["last_over"],
        "event_id": game["event_id"],
    }

class Handler(SimpleHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
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
                s=snapshot(snap_viewer(pid))
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
                        nb=mkbuild()
                        nb.update({"id":pid,"name":name,"last_seen":now(),"ready":False,
                                   "input":{"x":0,"y":0},"x":(s%2)*400-200,"y":(s//2)*400-200})
                        if game["phase"] == "playing":
                            nb["prot"] = now()+3
                        game["players"][s]=nb
                    else:
                        game["players"][s]["name"]=name; game["players"][s]["last_seen"]=now()
                    return self._json({"you":s+1})
                touch(pid); s=slot_of(pid)
                if self.path=="/api/input" and s>=0:
                    try:
                        ix=max(-1,min(1,float(d.get("x",0)))); iy=max(-1,min(1,float(d.get("y",0))))
                    except: ix,iy=0,0
                    game["players"][s]["input"]={"x":ix,"y":iy}
                    return self._json({"ok":True})
                if self.path=="/api/pick" and s>=0:
                    b=game["players"][s]
                    if b["pending"]:
                        opts=[o["id"] for o in b["pending"]]
                        oid=str(d.get("id",""))
                        if oid in opts:
                            apply_pick(s, oid)
                            b["pending"]=None
                            return self._json({"ok":True})
                    return self._json({"ok":False})
                if self.path=="/api/ready" and s>=0:
                    game["players"][s]["ready"]=True
                    return self._json({"ok":True})
                if self.path=="/api/leave":
                    if s>=0:
                        game["players"][s]=None
                    return self._json({"ok":True})
                if self.path=="/api/restart":
                    if game["phase"]=="over":
                        for p in game["players"]:
                            if p: p["ready"]=False
                        game["phase"]="waiting"
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
    srv.socket.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    print(f"\n  NEON HORDE running! 2-4 hunters, endless infinite field.\n  On this laptop:  http://localhost:{PORT}")
    for ip in lan_ips(): print(f"  Friend on same WiFi:  http://{ip}:{PORT}")
    print("\n  Survive. Revive. Draft. Boss every 4 minutes.\n")
    try: srv.serve_forever()
    except KeyboardInterrupt: print("\nbye!")
