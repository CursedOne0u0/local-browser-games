// Tank Duel LAN server (Go port of 2players/tank-duel/server.py).
// Routes and JSON are identical to the Python server.
package tank

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
	// repo layout: gameserver/bin/<bin> -> ../../local-browser-games/2players/tank-duel/public
	dir := filepath.Join(filepath.Dir(exe), "..", "..", "local-browser-games", "2players", "tank-duel", "public")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir
	}
	return "public"
}

// --- snapshot (field-for-field identical to server.py snapshot()) ---

type tankJSON struct {
	X     float64   `json:"x"`
	Y     float64   `json:"y"`
	Ang   float64   `json:"ang"`
	Score int       `json:"score"`
	Alive bool      `json:"alive"`
	Wpn   *string   `json:"wpn"`
	WpnT  float64   `json:"wpn_t"`
	Rail  *railJSON `json:"rail"`
}

type railJSON struct {
	T   float64 `json:"t"`
	Ang float64 `json:"ang"`
}

type snapshotJSON struct {
	V          string      `json:"v"`
	Phase      string      `json:"phase"`
	Countdown  int         `json:"countdown"`
	Round      int         `json:"round"`
	Maze       []string    `json:"maze"`
	Tanks      [2]tankJSON `json:"tanks"`
	Bullets    []any       `json:"bullets"`
	Mines      []any       `json:"mines"`
	Pickups    []any       `json:"pickups"`
	LastBlock  *BlockEv    `json:"last_block"`
	LastPickup *PickupEv   `json:"last_pickup"`
	LastShot   *ShotEv     `json:"last_shot"`
	LastBeam   *BeamEv     `json:"last_beam"`
	LastPop    *PopEv      `json:"last_pop"`
	BounceN    int         `json:"bounce_n"`
	Winner     int         `json:"winner"`
	Names      [2]string   `json:"names"`
	Ready      [2]bool     `json:"ready"`
	Connected  [2]bool     `json:"connected"`
	LastKill   *KillEv     `json:"last_kill"`
	Paused     bool        `json:"paused"`
	PausedBy   string      `json:"paused_by"`
	EventID    int         `json:"event_id"`
}

func (s *Server) snapshot(now float64) snapshotJSON {
	g := s.sim
	out := snapshotJSON{
		V: VERSION, Phase: g.Phase, Round: g.Round,
		BounceN: g.BounceN, Winner: g.Winner,
		LastBlock: g.LastBlock, LastPickup: g.LastPickup,
		LastShot: g.LastShot, LastBeam: g.LastBeam, LastPop: g.LastPop,
		LastKill: g.LastKill, Paused: g.Paused, PausedBy: g.PausedBy,
		EventID: g.EventID,
	}
	if g.Phase == "countdown" {
		out.Countdown = int(math.Max(1, math.Ceil(g.CountdownEnd-now)))
	}
	out.Maze = make([]string, Rows)
	for i := range Rows {
		out.Maze[i] = g.Maze[i]
	}
	for i := range 2 {
		t := g.Tanks[i]
		tj := tankJSON{
			X: r1(t.X), Y: r1(t.Y), Ang: r3(t.Ang),
			Score: t.Score, Alive: t.Alive,
			WpnT: math.Round(math.Max(0, t.WpnUntil-now)*10) / 10,
		}
		if now < t.WpnUntil && t.Wpn != "" {
			w := t.Wpn
			tj.Wpn = &w
		}
		if t.RailCharge > 0 {
			tj.Rail = &railJSON{
				T:   math.Round(t.RailCharge*100) / 100,
				Ang: r3(t.RailAng),
			}
		}
		out.Tanks[i] = tj
	}
	out.Bullets = []any{}
	for _, b := range g.Bullets {
		out.Bullets = append(out.Bullets, map[string]any{
			"x": r1(b.X), "y": r1(b.Y), "home": b.Home,
			"a": r3(math.Atan2(b.VY, b.VX)),
		})
	}
	out.Mines = []any{}
	for _, m := range g.Mines {
		out.Mines = append(out.Mines, map[string]any{
			"x": r1(m.X), "y": r1(m.Y), "owner": m.Owner, "armed": now >= m.ArmedAt,
		})
	}
	out.Pickups = []any{}
	for _, p := range g.Pickups {
		out.Pickups = append(out.Pickups, map[string]any{
			"x": r1(p.X), "y": r1(p.Y), "kind": p.Kind,
		})
	}
	for i := range 2 {
		if g.Players[i] != nil {
			out.Names[i] = g.Players[i].Name
			out.Ready[i] = g.Players[i].Ready
			out.Connected[i] = true
		}
	}
	return out
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
				g.Players[sl] = &Player{ID: pid, Name: name, LastSeen: now}
			} else {
				g.Players[sl].Name = name
				g.Players[sl].LastSeen = now
			}
			writeJSON(w, map[string]any{"you": sl + 1}, 200)
			return
		}
		g.Touch(pid, now)
		sl := g.SlotOf(pid)
		num := func(k string) float64 {
			if v, ok := body[k].(float64); ok {
				return v
			}
			return 0
		}
		switch {
		case hasSuffix(path, "/input") && sl >= 0:
			p := g.Players[sl]
			p.Fwd = clamp1(num("fwd"))
			p.Turn = clamp1(num("turn"))
			if f, ok := body["fire"].(bool); ok {
				p.Fire = f
			} else {
				p.Fire = false
			}
			writeJSON(w, map[string]any{"ok": true}, 200)
		case hasSuffix(path, "/ready") && sl >= 0:
			g.Players[sl].Ready = true
			writeJSON(w, map[string]any{"ok": true}, 200)
		case hasSuffix(path, "/input"), hasSuffix(path, "/ready"):
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
		case hasSuffix(path, "/restart"):
			if g.Phase == "over" {
				g.ResetScores()
				g.ResetTanks(now)
				for _, p := range g.Players {
					if p != nil {
						p.Ready = false
					}
				}
				g.Phase = "waiting"
				g.Winner = 0
				g.Round = 1
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
				p0, p1 := g.Players[0], g.Players[1]
				if g.Phase == "waiting" && p0 != nil && p1 != nil && p0.Ready && p1.Ready {
					g.Phase = "countdown"
					g.CountdownEnd = now + 2.4
					g.ResetScores()
					g.ResetTanks(now)
				}
				g.Step(dt, now)
			}
			s.mu.Unlock()
			time.Sleep(time.Second / 60)
		}
	}()

	fmt.Printf("\n  TANK DUEL running! (go build)\n  On this machine:  http://localhost:%d\n", port)
	for _, ip := range lanIPs() {
		fmt.Printf("  Friend on same WiFi:  http://%s:%d\n", ip, port)
	}
	fmt.Println("\n  W/S drive, A/D rotate, SPACE fire — first to 5 rounds wins!")
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
