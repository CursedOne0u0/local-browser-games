#!/usr/bin/env python3
"""Neon Pong Showdown — LAN 2-player server. Zero dependencies, stdlib only.
Run:  python3 server.py   (then open the printed LAN URL on both laptops)
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

PORT = int(os.environ.get("PORT", "3000"))
VERSION = "1.33"  # bump on every update; shown on the site
TAUNTS = {"gg": "GG! 🏓", "nice": "Nice shot! 🔥", "ouch": "Ouch! 😅",
          "whoops": "Whoops! 🙈", "lol": "LOL 😂", "rematch": "Rematch? 👀"}
TAUNT_CD = 2.5
W, H = 800, 500
WIN_SCORE = 7
BASE_PADDLE_H = 90
WIND_ACCEL = 120  # wind-rally vertical drift, px/s^2 (gentle, washes out on paddle hits)
PUBLIC = Path(__file__).parent / "public"

lock = threading.Lock()
game = {
    "phase": "waiting",  # waiting|countdown|playing|point|over
    "countdown_end": 0,
    "point_end": 0,
    "point_scored": 0,
    "players": [None, None],  # {id,name,y,vy,prev_y,prev_t,ready,last_seen,shield,frozen_until,od_until,od_cd,od_ready_at}
    "p1": {"y": 0.5, "h": BASE_PADDLE_H, "score": 0, "effect_until": 0},
    "p2": {"y": 0.5, "h": BASE_PADDLE_H, "score": 0, "effect_until": 0},
    "ball": {"x": W/2, "y": H/2, "vx": 0, "vy": 0, "speed": 420, "last_hit": 0, "spin": 0},
    "rally": 0,  # paddle hits since the last point
    "wind": 0,  # -1/0/+1: vertical drift this rally (wind round after every 3rd scored point)
    "powerup": None,
    "powerup_timer": 5,
    "serve_dir": 1,
    "winner": 0,
    "sudden": False,
    "serve_preview_until": 0,
    "serve_vx": 0,
    "serve_vy": 0,
    "serve_current_dir": 1,
    "serve_armed": False,
    "obstacle": None,  # {x,y,ang,len,phase,until}
    "ob_next": 0,
    "last_obounce": None,
    "ghost_until": 0,
    "ghost_hidden_for": 0,
    "settings": {"win": 7, "speed": 420, "obstacle": True},
    "last_taunt": None,
    "vortex": None,  # {x,y,until,id} gravity well
    "bot": False,  # AI holds P2 seat for solo play
    "bot_vy": 0.0, "bot_prev_y": 0.5, "bot_prev_t": 0.0, "bot_err": 0.0,
    "paused": False, "paused_by": "", "paused_since": 0,
    "event_id": 0,
    "last_power": None,  # {kind,by,id}
    "last_point": None,  # {scored,scores,id}
    "last_block": None,  # {by,id} shield save
}

def mkplayer(pid, name):
    return {"id": pid, "name": name, "y": 0.5, "vy": 0, "prev_y": 0.5, "prev_t": now(),
            "ready": False, "last_seen": now(), "shield": False,
            "frozen_until": 0, "od_until": 0, "od_ready_at": 0}

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
        if p and now() - p["last_seen"] > 8:
            game["players"][i] = None

def reset_positions():
    game["p1"]["y"] = 0.5; game["p2"]["y"] = 0.5
    game["p1"]["h"] = BASE_PADDLE_H; game["p2"]["h"] = BASE_PADDLE_H
    game["sudden"] = False
def reset_scores():
    game["p1"]["score"] = 0; game["p2"]["score"] = 0; game["winner"] = 0
    game["rally"] = 0
    game["sudden"] = False; game["last_block"] = None
def serve():
    b = game["ball"]
    b["x"] = W/2; b["y"] = 120 + random.random()*(H-240)
    ang = random.random()*0.6 - 0.3
    s = game["settings"]["speed"]  # default serve speed, reset every point
    b["speed"] = s; b["spin"] = 0; b["last_hit"] = 0; game["rally"] = 0
    vx = math.cos(ang)*s*game["serve_dir"]
    vy = math.sin(ang)*s + (-60 if random.random()<0.5 else 60)
    # hold the ball briefly so players see where it's going
    game["serve_current_dir"] = game["serve_dir"]
    game["serve_vx"] = vx; game["serve_vy"] = vy
    b["vx"] = 0; b["vy"] = 0
    game["serve_preview_until"] = now()+0.9
    game["serve_armed"] = True
    game["serve_dir"] *= -1
    # wind round: the serve after every 3rd scored point drifts vertically
    tot = game["p1"]["score"]+game["p2"]["score"]
    game["wind"] = random.choice([-1, 1]) if tot > 0 and tot % 3 == 0 else 0
    if game["wind"]:
        game["event_id"] += 1
        game["last_power"] = {"kind": "wind", "by": 0, "id": game["event_id"]}

def spawn_powerup():
    kinds = ["expand","shrink","turbo","slow","shield","freeze","magnet","ghost","swap","vortex"]
    game["powerup"] = {"x": W*0.3+random.random()*W*0.4, "y": 80+random.random()*(H-160),
                       "kind": random.choice(kinds), "born": now()}

def apply_powerup(kind, hitter):
    me = game["p1"] if hitter==1 else game["p2"]
    op = game["p2"] if hitter==1 else game["p1"]
    b = game["ball"]
    if kind=="expand": me["h"]=140; me["effect_until"]=now()+10
    elif kind=="shrink": op["h"]=52; op["effect_until"]=now()+10
    elif kind=="turbo":
        b["vx"]*=1.45; b["vy"]*=1.45; b["speed"]=min(900,b["speed"]*1.2)
    elif kind=="slow":
        b["vx"]*=0.65; b["vy"]*=0.65; b["speed"]=max(280,b["speed"]*0.85)
    elif kind=="shield":
        pl = game["players"][hitter-1]
        if pl: pl["shield"]=True
    elif kind=="freeze":
        foe = game["players"][1 if hitter==1 else 0]
        if foe: foe["frozen_until"]=now()+2.5
    elif kind=="magnet":
        pl = game["players"][hitter-1]
        if pl: pl["magnet_until"]=now()+8
    elif kind=="ghost":
        game["ghost_until"]=now()+2.5
        game["ghost_hidden_for"]=2 if hitter==1 else 1
    elif kind=="swap":
        game["p1"]["y"], game["p2"]["y"] = game["p2"]["y"], game["p1"]["y"]
        for i in (0, 1):
            pl = game["players"][i]
            if pl:
                key = "p1" if i == 0 else "p2"
                pl["y"] = game[key]["y"]; pl["prev_y"] = game[key]["y"]; pl["vy"] = 0
    elif kind=="vortex":
        game["event_id"] += 1
        game["vortex"] = {"x": max(120, min(W-120, b["x"])),
                          "y": max(80, min(H-80, b["y"])),
                          "until": now()+4.5, "id": game["event_id"]}
    game["event_id"]+=1
    game["last_power"]={"kind":kind,"by":hitter,"id":game["event_id"]}

def point(winner):
    # shield save: goal on owner's side blocked once
    if winner==2 and game["players"][0] and game["players"][0]["shield"]:
        game["players"][0]["shield"]=False
        game["event_id"]+=1
        game["last_block"]={"by":1,"id":game["event_id"]}
        game["phase"]="point"; game["point_scored"]=0; game["point_end"]=now()+1.0
        game["powerup"]=None
        return
    if winner==1 and game["players"][1] and game["players"][1]["shield"]:
        game["players"][1]["shield"]=False
        game["event_id"]+=1
        game["last_block"]={"by":2,"id":game["event_id"]}
        game["phase"]="point"; game["point_scored"]=0; game["point_end"]=now()+1.0
        game["powerup"]=None
        return
    if winner==1: game["p1"]["score"]+=1
    else: game["p2"]["score"]+=1
    game["rally"]=0
    game["powerup"]=None
    game["event_id"]+=1
    game["last_point"]={"scored":winner,"scores":[game["p1"]["score"],game["p2"]["score"]],"id":game["event_id"]}
    win = game["settings"]["win"]
    a, b = game["p1"]["score"], game["p2"]["score"]
    if ((a>=win or b>=win) and abs(a-b)>=2) or a>=win+4 or b>=win+4:
        game["phase"]="over"; game["winner"]=winner
    else:
        game["phase"]="point"; game["point_scored"]=winner; game["point_end"]=now()+1.2
        # sudden death at win-1 vs win-1: shrink arena, speed up
        if game["p1"]["score"]>=win-1 and game["p2"]["score"]>=win-1:
            game["sudden"]=True
            game["event_id"]+=1
            game["last_power"]={"kind":"sudden","by":0,"id":game["event_id"]}

def obstacle_update():
    t = now()
    if not game["settings"].get("obstacle", True):
        game["obstacle"] = None
        return
    ob = game["obstacle"]
    if ob is None:
        if t >= game["ob_next"]:
            game["obstacle"] = {
                "x": W/2 + (random.random()-0.5)*140,
                "y": H/2 + (random.random()-0.5)*140,
                "ang": random.random()*math.pi,  # random tilt each spawn (static, no spin)
                "len": 110 + random.random()*70,
                "phase": "warn_in",
                "until": t + 1.2,
            }
        return
    if t >= ob["until"]:
        if ob["phase"] == "warn_in":
            ob["phase"] = "active"; ob["until"] = t + 6 + random.random()*3
        elif ob["phase"] == "active":
            ob["phase"] = "warn_out"; ob["until"] = t + 0.8
        elif ob["phase"] == "warn_out":
            game["obstacle"] = None
            game["ob_next"] = t + 8 + random.random()*6

def obstacle_collide():
    ob = game["obstacle"]
    if not ob or ob["phase"] != "active": return
    b = game["ball"]
    dx = math.cos(ob["ang"]); dy = math.sin(ob["ang"])
    hl = ob["len"]/2
    rx = b["x"]-ob["x"]; ry = b["y"]-ob["y"]
    proj = max(-hl, min(hl, rx*dx+ry*dy))
    cx = ob["x"]+dx*proj; cy = ob["y"]+dy*proj
    nx = b["x"]-cx; ny = b["y"]-cy
    dist = math.hypot(nx, ny)
    R = 9
    if dist < R + 2:
        if dist < 1e-6: nx, ny = -dy, dx
        else: nx /= dist; ny /= dist
        dot = b["vx"]*nx + b["vy"]*ny
        if dot < 0:  # moving into the beam: reflect
            b["vx"] -= 2*dot*nx; b["vy"] -= 2*dot*ny
            sp = math.hypot(b["vx"], b["vy"]) or 1
            tgt = min(920, b["speed"])
            b["vx"] *= tgt/sp; b["vy"] *= tgt/sp
            b["x"] = cx + nx*(R+3); b["y"] = cy + ny*(R+3)
            b["spin"] = 0
            game["event_id"] += 1
            game["last_obounce"] = {"id": game["event_id"]}

def bot_update(dt):
    # solo-mode AI drives P2: predicts with wall bounces, capped speed + aim error
    if not game["bot"]: return
    b = game["ball"]
    target = 0.5
    if b["vx"] > 50:
        t_hit = ((W-30-14) - b["x"]) / b["vx"]
        if t_hit > 0:
            py = b["y"] + b["vy"]*min(t_hit, 3)
            span = H-20
            m = (py-10) % (2*span)
            py = 10 + m if m <= span else 10 + 2*span - m
            if random.random() < 0.03:
                game["bot_err"] = (random.random()-0.5)*0.14
            target = max(0, min(1, py/H + game["bot_err"]))
    cur = game["p2"]["y"]
    maxstep = 1.15*dt
    d = max(-maxstep, min(maxstep, target-cur))
    game["p2"]["y"] = max(0, min(1, cur+d))
    tnow = now()
    inst = d/max(1e-3, tnow-game.get("bot_prev_t", tnow))
    game["bot_vy"] = game.get("bot_vy", 0)*0.6 + max(-4, min(4, inst))*0.4
    game["bot_prev_y"] = game["p2"]["y"]; game["bot_prev_t"] = tnow

def step(dt):
    if game["phase"]=="countdown" and now()>=game["countdown_end"]:
        game["phase"]="playing"; serve()
        return
    if game["phase"]=="point" and now()>=game["point_end"]:
        game["phase"]="playing"
        game["serve_dir"]=-1 if game["point_scored"]==1 else 1
        serve()
        return
    if game["phase"]!="playing": return
    t=now()
    obstacle_update()
    bot_update(dt)
    # serve preview: hold ball, then release in the shown direction
    if game.get("serve_armed"):
        if t < game["serve_preview_until"]:
            game["ball"]["vx"] = 0; game["ball"]["vy"] = 0
            return  # hold position; client shows direction arrow
        game["ball"]["vx"] = game["serve_vx"]; game["ball"]["vy"] = game["serve_vy"]
        game["serve_armed"] = False
    # overdrive: paddle 1.6x while active
    for i, key in ((0, "p1"), (1, "p2")):
        pl = game["players"][i]
        base = 64 if game["sudden"] else BASE_PADDLE_H
        if pl and t < pl.get("od_until", 0):
            game[key]["h"] += (base*1.6 - game[key]["h"])*0.2
        elif t>game[key]["effect_until"]:
            game[key]["h"] += (base - game[key]["h"])*0.05
    if game["sudden"]:
        # sudden-death rally heat: ball slowly accelerates
        game["ball"]["vx"] *= (1+0.0015); game["ball"]["vy"] *= (1+0.0015)
    b=game["ball"]
    b["x"]+=b["vx"]*dt; b["y"]+=b["vy"]*dt
    # wind-round drift (washes out on paddle hits, which reset vy)
    if game["wind"]:
        b["vy"] += game["wind"]*WIND_ACCEL*dt
    # spin curves the ball (kept subtle)
    b["vy"] += b.get("spin",0)*dt*120
    b["spin"] = b.get("spin",0)*0.99
    if b["y"]<10: b["y"]=10; b["vy"]=abs(b["vy"])
    if b["y"]>H-10: b["y"]=H-10; b["vy"]=-abs(b["vy"])
    p1x, p2x = 30, W-30-14
    p1ypx, p2ypx = game["p1"]["y"]*H, game["p2"]["y"]*H
    p1v = (game["players"][0] or {}).get("vy", 0) if game["players"][0] else 0
    p2v = game["bot_vy"] if game["bot"] else ((game["players"][1] or {}).get("vy", 0) if game["players"][1] else 0)
    # magnet paddles bend incoming balls on their own half toward them
    if game["players"][0] and t < game["players"][0].get("magnet_until", 0) and b["x"] < W/2 and b["vx"] < 0:
        b["vy"] += max(-1, min(1, (game["p1"]["y"]*H - b["y"])/60))*550*dt
    if game["players"][1] and t < game["players"][1].get("magnet_until", 0) and b["x"] > W/2 and b["vx"] > 0:
        b["vy"] += max(-1, min(1, (game["p2"]["y"]*H - b["y"])/60))*550*dt
    vx_ = game["vortex"]
    if vx_ and t < vx_["until"]:
        dx = vx_["x"]-b["x"]; dy = vx_["y"]-b["y"]
        dist = math.hypot(dx, dy)
        if dist < 16:  # slingshot ejection
            sp = min(920, math.hypot(b["vx"], b["vy"])*1.25 + 40)
            b["speed"] = sp
            nx, ny = (dx/dist, dy/dist) if dist > 1e-6 else (1, 0)
            b["vx"] = -nx*sp*0.7 + b["vx"]*0.3
            b["vy"] = -ny*sp*0.7 + b["vy"]*0.3
            n2 = math.hypot(b["vx"], b["vy"]) or 1
            b["vx"] *= sp/n2; b["vy"] *= sp/n2
            b["x"] = vx_["x"] - nx*24; b["y"] = vx_["y"] - ny*24
        elif dist < 320:  # gravity pull, capped
            pull = min(1400, 90000/max(dist, 30))
            b["vx"] += dx/dist*pull*dt; b["vy"] += dy/dist*pull*dt
            sp = math.hypot(b["vx"], b["vy"])
            cap = min(920, b["speed"]*1.35)
            if sp > cap: b["vx"] *= cap/sp; b["vy"] *= cap/sp
    elif vx_:
        game["vortex"] = None
    if b["vx"]<0 and b["x"]-8<p1x+14 and b["x"]-8>p1x-12 and abs(b["y"]-p1ypx)<game["p1"]["h"]/2+8:
        b["x"]=p1x+14+8
        rel=(b["y"]-p1ypx)/(game["p1"]["h"]/2+1e-6)
        bounce=max(-1,min(1,rel))*0.9
        sp=min(920, math.hypot(b["vx"],b["vy"])*1.045+8)
        b["speed"]=sp; b["vx"]=math.cos(bounce)*sp; b["vy"]=math.sin(bounce)*sp + p1v*H*0.12; b["last_hit"]=1; game["rally"]+=1
        b["spin"]=max(-0.5,min(0.5,p1v*0.7))
    if b["vx"]>0 and b["x"]+8>p2x and b["x"]+8<p2x+14+12 and abs(b["y"]-p2ypx)<game["p2"]["h"]/2+8:
        b["x"]=p2x-8
        rel=(b["y"]-p2ypx)/(game["p2"]["h"]/2+1e-6)
        bounce=max(-1,min(1,rel))*0.9
        sp=min(920, math.hypot(b["vx"],b["vy"])*1.045+8)
        b["speed"]=sp; b["vx"]=-math.cos(bounce)*sp; b["vy"]=math.sin(bounce)*sp + p2v*H*0.12; b["last_hit"]=2; game["rally"]+=1
        b["spin"]=max(-0.5,min(0.5,p2v*0.7))
    obstacle_collide()
    if game["powerup"] and b["last_hit"]:
        dx=b["x"]-game["powerup"]["x"]; dy=b["y"]-game["powerup"]["y"]
        if dx*dx+dy*dy<28*28:
            apply_powerup(game["powerup"]["kind"], b["last_hit"])
            game["powerup"]=None; game["powerup_timer"]=4+random.random()*4
    else:
        game["powerup_timer"]-=dt
        if game["powerup_timer"]<=0 and not game["powerup"]:
            spawn_powerup(); game["powerup_timer"]=6+random.random()*5
    if game["powerup"] and now()-game["powerup"]["born"]>12:
        game["powerup"]=None; game["powerup_timer"]=3
    if b["x"]<-20: point(2)
    elif b["x"]>W+20: point(1)

def game_loop():
    last=time.time()
    while True:
        t=time.time(); dt=min(0.05,t-last); last=t
        with lock:
            free_stale()
            if game["paused"]:
                pass  # frozen for all: no sim, no transitions, no deadlines
            else:
                # auto-start countdown when both humans ready, or human P1 + bot
                p0,p1=game["players"]
                foe_ready = (p1 and p1["ready"]) or (game["bot"] and not p1)
                if game["phase"]=="waiting" and p0 and p0["ready"] and foe_ready:
                    game["phase"]="countdown"; game["countdown_end"]=now()+2.4
                    reset_scores(); reset_positions()
                    for p in (p0, p1):
                        if not p: continue
                        p["shield"]=False; p["frozen_until"]=0; p["od_until"]=0; p["od_ready_at"]=0; p["vy"]=0; p["magnet_until"]=0
                    game["bot_vy"]=0.0; game["bot_err"]=0.0; game["bot_prev_y"]=0.5
                    game["ghost_until"]=0; game["ghost_hidden_for"]=0; game["vortex"]=None
                    game["ball"]["speed"]=game["settings"]["speed"]; game["ball"]["last_hit"]=0; game["ball"]["spin"]=0
                    game["wind"]=0
                    game["obstacle"]=None; game["ob_next"]=now()+6; game["last_obounce"]=None
                    game["serve_dir"]=-1 if random.random()<0.5 else 1
                    game["powerup"]=None; game["powerup_timer"]=5
                step(dt)
        time.sleep(1/60)

def shift_paused(d):
    # thaw every absolute deadline by the paused duration (presence timers untouched)
    game["countdown_end"] += d; game["point_end"] += d
    game["serve_preview_until"] += d; game["ob_next"] += d; game["ghost_until"] += d
    if game["obstacle"] and game["obstacle"].get("until"): game["obstacle"]["until"] += d
    if game["vortex"] and game["vortex"].get("until"): game["vortex"]["until"] += d
    if game["powerup"] and game["powerup"].get("born"): game["powerup"]["born"] += d
    for p in game["players"]:
        if not p: continue
        for k in ("effect_until", "frozen_until", "magnet_until", "od_until", "od_ready_at"):
            if p.get(k): p[k] += d

def snapshot():
    cd = 0
    if game["phase"]=="countdown": cd=max(1, math.ceil(game["countdown_end"]-now()))
    t = now()
    def pinfo(i):
        p = game["players"][i]
        key = "p1" if i == 0 else "p2"
        base = 64 if game["sudden"] else BASE_PADDLE_H
        h = game[key]["h"]
        fxd = 1 if h > base+2 else (-1 if h < base-2 else 0)
        fx = max(0, game[key].get("effect_until", 0)-t)
        if not p: return {"shield": False, "frozen": False, "od": 0, "od_cd": 0,
                          "fr": 0, "fx": 0, "fxd": 0, "mag": 0}
        return {"shield": bool(p.get("shield")), "frozen": t < p.get("frozen_until", 0),
                "fr": max(0, p.get("frozen_until", 0)-t),
                "od": max(0, p.get("od_until", 0)-t), "od_cd": max(0, p.get("od_ready_at", 0)-t),
                "fx": fx, "fxd": fxd,
                "mag": max(0, p.get("magnet_until", 0)-t)}
    return {
        "v": VERSION,
        "phase": game["phase"],
        "countdown": cd,
        "sudden": game["sudden"],
        "wind": game.get("wind", 0),
        "preview": game["phase"]=="playing" and bool(game.get("serve_armed")) and t < game.get("serve_preview_until", 0),
        "serve_dir": game.get("serve_current_dir", 1),
        "p1": {"y": game["p1"]["y"], "h": game["p1"]["h"], "score": game["p1"]["score"], **pinfo(0)},
        "p2": {"y": game["p2"]["y"], "h": game["p2"]["h"], "score": game["p2"]["score"], **pinfo(1)},
        "ball": {"x": round(game["ball"]["x"],1), "y": round(game["ball"]["y"],1),
                 "vx": round(game["ball"]["vx"],1), "vy": round(game["ball"]["vy"],1)},
        "rally": game["rally"],
        "powerup": game["powerup"],
        "obstacle": game["obstacle"],
        "last_obounce": game["last_obounce"],
        "ghost_for": game["ghost_hidden_for"] if t < game["ghost_until"] else 0,
        "ghost": max(0, game["ghost_until"]-t),
        "vortex": ({k: (round(v,1) if isinstance(v, float) else v) for k, v in game["vortex"].items()}
                   if game["vortex"] and t < game["vortex"]["until"] else None),
        "settings": dict(game["settings"]),
        "last_taunt": game["last_taunt"],
        "winner": game["winner"],
        "paused": game["paused"],
        "paused_by": game["paused_by"],
        "bot": game["bot"],
        "names": [(game["players"][0] or {}).get("name","") or "",
                  (game["players"][1] or {}).get("name","") or ("AI 🤖" if game["bot"] else "")],
        "ready": [bool(game["players"][0] and game["players"][0]["ready"]),
                  bool(game["players"][1] and game["players"][1]["ready"]) or game["bot"]],
        "connected": [bool(game["players"][0]), bool(game["players"][1])],
        "last_power": game["last_power"],
        "last_point": game["last_point"],
        "last_block": game["last_block"],
        "event_id": game["event_id"],
    }

class Handler(SimpleHTTPRequestHandler):
    protocol_version = "HTTP/1.1"  # keep-alive: no TCP+TLS handshake per 60Hz poll
    def __init__(self,*a,**kw): super().__init__(*a, directory=str(PUBLIC), **kw)
    def log_message(self,*a): pass
    def handle_one_request(self):
        # 60Hz polling means the browser sometimes drops a response
        # mid-write (refresh, superseded poll) — not worth a traceback
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
        if parsed.path in ("/","/index.html"):
            return super().do_GET()
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
                        game["players"][s]=mkplayer(pid, name)
                        if s == 1 and game["bot"]:
                            game["bot"] = False  # human takes the seat back
                    else:
                        game["players"][s]["name"]=name; game["players"][s]["last_seen"]=now()
                    return self._json({"you":s+1})
                touch(pid); s=slot_of(pid)
                if self.path=="/api/move" and s>=0:
                    pl = game["players"][s]
                    if now() < pl.get("frozen_until", 0):
                        return self._json({"ok":True,"frozen":True})
                    try: y=max(0,min(1,float(d.get("y",0.5))))
                    except: y=0.5
                    t = now()
                    dt = max(1e-3, t - pl.get("prev_t", t))
                    vy = (y - pl.get("prev_y", y)) / dt
                    vy = max(-4, min(4, vy))
                    pl["vy"] = pl.get("vy", 0)*0.6 + vy*0.4
                    pl["prev_y"] = y; pl["prev_t"] = t
                    pl["y"]=y
                    if s==0: game["p1"]["y"]=y
                    else: game["p2"]["y"]=y
                    return self._json({"ok":True})
                if self.path=="/api/overdrive" and s>=0:
                    pl = game["players"][s]
                    t = now()
                    if t >= pl.get("od_ready_at", 0) and game["phase"]=="playing":
                        pl["od_until"]=t+1.6; pl["od_ready_at"]=t+8
                        game["event_id"]+=1
                        game["last_power"]={"kind":"overdrive","by":s+1,"id":game["event_id"]}
                        return self._json({"ok":True})
                    return self._json({"ok":False,"cd":max(0,pl.get("od_ready_at",0)-t)})
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
                if self.path=="/api/bot" and s>=0:
                    want = bool(d.get("on", not game["bot"]))
                    if want:
                        if game["players"][1] or game["phase"] != "waiting":
                            return self._json({"ok":False,"err":"seat taken or mid-match"})
                        game["bot"] = True
                    else:
                        game["bot"] = False
                    return self._json({"ok":True,"bot":game["bot"]})
                if self.path=="/api/taunt" and s>=0:
                    key = str(d.get("msg", ""))
                    if key not in TAUNTS:
                        return self._json({"ok":False,"err":"nope"})
                    pl = game["players"][s]
                    t = now()
                    if t < pl.get("taunt_ready_at", 0):
                        return self._json({"ok":False,"cd":round(pl["taunt_ready_at"]-t,1)})
                    pl["taunt_ready_at"] = t + TAUNT_CD
                    game["event_id"] += 1
                    game["last_taunt"] = {"by": s+1, "key": key,
                                          "text": TAUNTS[key], "id": game["event_id"]}
                    return self._json({"ok":True})
                if self.path=="/api/settings" and s>=0:
                    if game["phase"] != "waiting":
                        return self._json({"ok":False,"err":"mid-match"})
                    if d.get("win") in (3,5,7,11): game["settings"]["win"]=d["win"]
                    if d.get("speed") in (320,420,520): game["settings"]["speed"]=d["speed"]
                    if "obstacle" in d: game["settings"]["obstacle"]=bool(d["obstacle"])
                    if not game["settings"]["obstacle"]: game["obstacle"]=None
                    return self._json({"ok":True,"settings":dict(game["settings"])})
                if self.path=="/api/restart":
                    if game["phase"]=="over":
                        reset_scores(); reset_positions()
                        game["obstacle"]=None; game["last_obounce"]=None
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
    print(f"\n  NEON PONG SHOWDOWN running!\n  On this laptop:  http://localhost:{PORT}")
    _ips = lan_ips()
    for ip in _ips: print(f"  Friend on same WiFi:  http://{ip}:{PORT}")
    if _ips:
        print("  Scan to join (same WiFi):")
        print_qr(f"http://{_ips[0]}:{PORT}")
    print(f"\n  1. Host runs:  python3 server.py\n  2. Friend opens the LAN URL above (same WiFi)\n  3. Both enter names -> Join -> Ready -> first to {game['settings']['win']}, win by 2 (changeable in lobby)!\n")
    try: srv.serve_forever()
    except KeyboardInterrupt: print("\nbye!")
