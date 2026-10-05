// Neon Pong Showdown LAN server (Go port of hybrid1-2/neon-pong/server.py).
// Routes and JSON are identical to the Python server.
package pong

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Server struct {
	mu  sync.Mutex
	sim *Sim
}

func writeJSON(w http.ResponseWriter, obj any, code int) {
	b, _ := json.Marshal(obj)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprint(len(b)))
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func publicDir() string {
	if d := os.Getenv("GAMESERVER_PUBLIC"); d != "" {
		return d
	}
	exe, err := os.Executable()
	if err != nil {
		return "public"
	}
	// repo layout: gameserver/bin/<bin> -> ../../local-browser-games/hybrid1-2/neon-pong/public
	dir := filepath.Join(filepath.Dir(exe), "..", "..", "local-browser-games",
		"hybrid1-2", "neon-pong", "public")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir
	}
	return "public"
}

// --- snapshot ---

type paddleJSON struct {
	Y     float64 `json:"y"`
	H     float64 `json:"h"`
	Score int     `json:"score"`
	Shield bool   `json:"shield"`
	Frozen bool   `json:"frozen"`
	Od    float64 `json:"od"`
	OdCD  float64 `json:"od_cd"`
	Fr    float64 `json:"fr"`
	Fx    float64 `json:"fx"`
	Fxd   int     `json:"fxd"`
	Mag   float64 `json:"mag"`
}

func (s *Server) pinfo(i int, now float64) paddleJSON {
	g := s.sim
	var key *Paddle
	if i == 0 {
		key = g.P1
	} else {
		key = g.P2
	}
	base := BasePaddleH
	if g.Sudden {
		base = 64
	}
	fxd := 0
	if key.H > base+2 {
		fxd = 1
	} else if key.H < base-2 {
		fxd = -1
	}
	fx := math.Max(0, key.EffectUntil-now)
	out := paddleJSON{Y: key.Y, H: key.H, Score: key.Score, Fx: fx, Fxd: fxd}
	if p := g.Players[i]; p != nil {
		out.Shield = p.Shield
		out.Frozen = now < p.FrozenUntil
		out.Fr = math.Max(0, p.FrozenUntil-now)
		out.Od = math.Max(0, p.OdUntil-now)
		out.OdCD = math.Max(0, p.OdReadyAt-now)
		out.Mag = math.Max(0, p.MagnetUntil-now)
	}
	return out
}

func (s *Server) snapshot(now float64) map[string]any {
	g := s.sim
	cd := 0
	if g.Phase == "countdown" {
		cd = int(math.Max(1, math.Ceil(g.CountdownEnd-now)))
	}
	preview := g.Phase == "playing" && g.ServeArmed && now < g.ServePreviewUntil
	var vortex any
	if g.Vortex != nil && now < g.Vortex.Until {
		vortex = map[string]any{
			"x": r1(g.Vortex.X), "y": r1(g.Vortex.Y),
			"until": r1(g.Vortex.Until), "id": g.Vortex.ID,
		}
	}
	var powerup any
	if g.Powerup != nil {
		powerup = map[string]any{"x": g.Powerup.X, "y": g.Powerup.Y,
			"kind": g.Powerup.Kind, "born": g.Powerup.Born}
	}
	var obstacle any
	if g.Obstacle != nil {
		obstacle = map[string]any{"x": g.Obstacle.X, "y": g.Obstacle.Y,
			"ang": g.Obstacle.Ang, "len": g.Obstacle.Len,
			"phase": g.Obstacle.Phase, "until": g.Obstacle.Until}
	}
	var ghostFor int
	if now < g.GhostUntil {
		ghostFor = g.GhostHiddenFor
	}
	names := [2]string{}
	ready := [2]bool{}
	connected := [2]bool{}
	for i := range 2 {
		if p := g.Players[i]; p != nil {
			names[i] = p.Name
			ready[i] = p.Ready
			connected[i] = true
		}
	}
	if names[1] == "" && g.Bot {
		names[1] = "AI 🤖"
	}
	ready[1] = ready[1] || g.Bot
	return map[string]any{
		"v": VERSION,
		"phase": g.Phase,
		"countdown": cd,
		"sudden": g.Sudden,
		"wind": g.Wind,
		"preview": preview,
		"serve_dir": g.ServeCurrentDir,
		"p1": s.pinfo(0, now),
		"p2": s.pinfo(1, now),
		"ball": map[string]any{"x": r1(g.Ball.X), "y": r1(g.Ball.Y),
			"vx": r1(g.Ball.VX), "vy": r1(g.Ball.VY)},
		"rally": g.Rally,
		"powerup": powerup,
		"obstacle": obstacle,
		"last_obounce": g.LastBounce,
		"ghost_for": ghostFor,
		"ghost": math.Max(0, g.GhostUntil-now),
		"vortex": vortex,
		"settings": map[string]any{"win": g.Settings.Win, "speed": g.Settings.Speed,
			"obstacle": g.Settings.Obstacle},
		"last_taunt": g.LastTaunt,
		"winner": g.Winner,
		"paused": g.Paused,
		"paused_by": g.PausedBy,
		"bot": g.Bot,
		"names": names,
		"ready": ready,
		"connected": connected,
		"last_power": g.LastPower,
		"last_point": g.LastPoint,
		"last_block": g.LastBlock,
		"event_id": g.EventID,
	}
}

func Run(port int) {
	s := &Server{sim: New()}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(publicDir())))
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		pid := r.URL.Query().Get("id")
		now := unixNow()
		s.mu.Lock()
		s.sim.Touch(pid, now)
		out := s.snapshot(now)
		s.mu.Unlock()
		writeJSON(w, out, 200)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		dec := json.NewDecoder(r.Body)
		_ = dec.Decode(&body)
		if body == nil {
			body = map[string]any{}
		}
		sstr := func(k string) string {
			if v, ok := body[k].(string); ok {
				return v
			}
			return ""
		}
		num := func(k string) float64 {
			if v, ok := body[k].(float64); ok {
				return v
			}
			return 0
		}
		pid := sstr("clientId")
		if len(pid) > 32 {
			pid = pid[:32]
		}
		now := unixNow()
		s.mu.Lock()
		defer s.mu.Unlock()
		g := s.sim
		path := r.URL.Path
		switch {
		case hasSuffix(path, "/join"):
			name := sstr("name")
			if len(name) > 32 {
				name = name[:32]
			}
			if name == "" {
				name = "Player"
			}
			sl := g.SlotOf(pid)
			if sl < 0 {
				g.FreeStale(now)
				sl = -1
				for i := range 2 {
					if g.Players[i] == nil {
						sl = i
						break
					}
				}
				if sl < 0 {
					writeJSON(w, map[string]any{"you": 0}, 200)
					return
				}
				g.Players[sl] = &Player{ID: pid, Name: name, Y: 0.5,
					PrevY: 0.5, PrevT: now, LastSeen: now}
				if sl == 1 && g.Bot {
					g.Bot = false
				}
			} else {
				g.Players[sl].Name = name
				g.Players[sl].LastSeen = now
			}
			writeJSON(w, map[string]any{"you": sl + 1}, 200)
			return
		}
		g.Touch(pid, now)
		sl := g.SlotOf(pid)
		switch {
		case hasSuffix(path, "/move") && sl >= 0:
			pl := g.Players[sl]
			if now < pl.FrozenUntil {
				writeJSON(w, map[string]any{"ok": true, "frozen": true}, 200)
				break
			}
			y := num("y")
			if _, ok := body["y"].(float64); !ok {
				y = 0.5
			}
			y = math.Max(0, math.Min(1, y))
			dt := now - pl.PrevT
			if dt < 1e-3 {
				dt = 1e-3
			}
			vy := (y - pl.PrevY) / dt
			vy = math.Max(-4, math.Min(4, vy))
			pl.VY = pl.VY*0.6 + vy*0.4
			pl.PrevY = y
			pl.PrevT = now
			pl.Y = y
			if sl == 0 {
				g.P1.Y = y
			} else {
				g.P2.Y = y
			}
			writeJSON(w, map[string]any{"ok": true}, 200)
		case hasSuffix(path, "/overdrive") && sl >= 0:
			pl := g.Players[sl]
			if now >= pl.OdReadyAt && g.Phase == "playing" {
				pl.OdUntil = now + 1.6
				pl.OdReadyAt = now + 8
				g.EventID++
				g.LastPower = &PowerEv{Kind: "overdrive", By: sl + 1, ID: g.EventID}
				writeJSON(w, map[string]any{"ok": true}, 200)
			} else {
				writeJSON(w, map[string]any{"ok": false,
					"cd": math.Max(0, pl.OdReadyAt-now)}, 200)
			}
		case hasSuffix(path, "/move"), hasSuffix(path, "/overdrive"):
			writeJSON(w, map[string]any{"ok": false}, 400)
		case hasSuffix(path, "/ready") && sl >= 0:
			g.Players[sl].Ready = true
			writeJSON(w, map[string]any{"ok": true}, 200)
		case hasSuffix(path, "/ready"):
			writeJSON(w, map[string]any{"ok": false}, 400)
		case hasSuffix(path, "/leave"):
			if sl >= 0 {
				g.Players[sl] = nil
			}
			writeJSON(w, map[string]any{"ok": true}, 200)
		case hasSuffix(path, "/pause"):
			if g.Paused {
				g.ShiftPaused(now - g.PausedSince)
				g.Paused = false
				g.PausedBy = ""
			} else {
				var nm string
				if sl >= 0 && g.Players[sl] != nil {
					nm = g.Players[sl].Name
				}
				if nm == "" {
					nm = "Someone"
				}
				g.Paused = true
				g.PausedBy = nm
				g.PausedSince = now
			}
			writeJSON(w, map[string]any{"ok": true, "paused": g.Paused, "by": g.PausedBy}, 200)
		case hasSuffix(path, "/bot") && sl >= 0:
			want := !g.Bot
			if v, ok := body["on"].(bool); ok {
				want = v
			}
			if want {
				if g.Players[1] != nil || g.Phase != "waiting" {
					writeJSON(w, map[string]any{"ok": false, "err": "seat taken or mid-match"}, 200)
				} else {
					g.Bot = true
					writeJSON(w, map[string]any{"ok": true, "bot": g.Bot}, 200)
				}
			} else {
				g.Bot = false
				writeJSON(w, map[string]any{"ok": true, "bot": g.Bot}, 200)
			}
		case hasSuffix(path, "/bot"):
			writeJSON(w, map[string]any{"ok": false}, 400)
		case hasSuffix(path, "/taunt") && sl >= 0:
			key := sstr("msg")
			text, ok := Taunts[key]
			if !ok {
				writeJSON(w, map[string]any{"ok": false, "err": "nope"}, 200)
				break
			}
			pl := g.Players[sl]
			if now < pl.TauntReadyAt {
				writeJSON(w, map[string]any{"ok": false,
					"cd": math.Round((pl.TauntReadyAt-now)*10) / 10}, 200)
				break
			}
			pl.TauntReadyAt = now + TauntCD
			g.EventID++
			g.LastTaunt = &TauntEv{By: sl + 1, Key: key, Text: text, ID: g.EventID}
			writeJSON(w, map[string]any{"ok": true}, 200)
		case hasSuffix(path, "/taunt"):
			writeJSON(w, map[string]any{"ok": false}, 400)
		case hasSuffix(path, "/settings") && sl >= 0:
			if g.Phase != "waiting" {
				writeJSON(w, map[string]any{"ok": false, "err": "mid-match"}, 200)
				break
			}
			if wv := int(num("win")); wv == 3 || wv == 5 || wv == 7 || wv == 11 {
				// values arrive as float; accept only exact ints
				if _, ok := body["win"].(float64); ok {
					g.Settings.Win = wv
				}
			}
			if sv := int(num("speed")); sv == 320 || sv == 420 || sv == 520 {
				if _, ok := body["speed"].(float64); ok {
					g.Settings.Speed = sv
				}
			}
			if _, ok := body["obstacle"]; ok {
				ob := false
				if v, ok := body["obstacle"].(bool); ok {
					ob = v
				}
				g.Settings.Obstacle = ob
				if !ob {
					g.Obstacle = nil
				}
			}
			writeJSON(w, map[string]any{"ok": true, "settings": map[string]any{
				"win": g.Settings.Win, "speed": g.Settings.Speed,
				"obstacle": g.Settings.Obstacle}}, 200)
		case hasSuffix(path, "/settings"):
			writeJSON(w, map[string]any{"ok": false}, 400)
		case hasSuffix(path, "/restart"):
			if g.Phase == "over" {
				g.ResetScores()
				g.ResetPositions()
				g.Obstacle = nil
				g.LastBounce = nil
				for _, p := range g.Players {
					if p != nil {
						p.Ready = false
					}
				}
				g.Phase = "waiting"
				g.Winner = 0
			}
			writeJSON(w, map[string]any{"ok": true}, 200)
		default:
			writeJSON(w, map[string]any{"ok": false}, 400)
		}
	})

	go func() {
		last := float64(time.Now().UnixNano()) / 1e9
		for {
			t := float64(time.Now().UnixNano()) / 1e9
			dt := t - last
			if dt > 0.05 {
				dt = 0.05
			}
			last = t
			now := unixNow()
			s.mu.Lock()
			g := s.sim
			g.FreeStale(now)
			if !g.Paused {
				if g.ShouldStart() {
					g.EnterCountdown(now)
				}
				g.Step(dt, now)
			}
			s.mu.Unlock()
			time.Sleep(time.Second / 60)
		}
	}()

	fmt.Printf("\n  NEON PONG SHOWDOWN running! (go build)\n  On this machine:  http://localhost:%d\n", port)
	for _, ip := range lanIPs() {
		fmt.Printf("  Friend on same WiFi:  http://%s:%d\n", ip, port)
	}
	fmt.Println("\n  Both enter names -> Join -> Ready -> first to win, win by 2!")
	srv := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", port), Handler: mux}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Println("server error:", err)
		os.Exit(1)
	}
}

func hasSuffix(path, sfx string) bool {
	return len(path) >= len(sfx) && path[len(path)-len(sfx):] == sfx
}

func lanIPs() []string {
	var out []string
	seen := map[string]bool{}
	add := func(ip string) {
		if ip != "" && !seen[ip] {
			seen[ip] = true
			out = append(out, ip)
		}
	}
	if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			add(addr.IP.String())
		}
		conn.Close()
	}
	if host, err := os.Hostname(); err == nil {
		if addrs, err := net.LookupIP(host); err == nil {
			for _, a := range addrs {
				if v4 := a.To4(); v4 != nil && !v4.IsLoopback() {
					add(v4.String())
				}
			}
		}
	}
	return out
}
