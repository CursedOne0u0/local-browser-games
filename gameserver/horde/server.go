// Neon Horde LAN server (Go port of 2players/neon-horde/server.py).
// Routes and JSON are identical to the Python server.
package horde

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
	// repo layout: gameserver/bin/<bin> -> ../../local-browser-games/2players/neon-horde/public
	dir := filepath.Join(filepath.Dir(exe), "..", "..", "local-browser-games",
		"2players", "neon-horde", "public")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir
	}
	return "public"
}

var kindCode = map[string]int{
	"chaser": 0, "darter": 1, "brute": 2, "boss": 3,
	"splitter": 4, "charger": 5, "goblin": 6,
}
var modeCode = map[string]int{"chase": 0, "wind": 1, "dash": 2, "flee": 3}

func (s *Server) snapViewer(pid string) int {
	if sl := s.sim.SlotOf(pid); sl >= 0 && s.sim.Players[sl] != nil {
		return sl
	}
	if occ := s.sim.Occupied(); len(occ) > 0 {
		return occ[0]
	}
	return -1
}

func (s *Server) snapshot(viewer int, now float64) map[string]any {
	g := s.sim
	cd := 0
	if g.Phase == "countdown" {
		cd = int(math.Max(1, math.Ceil(g.CountdownEnd-now)))
	}
	var vx, vy float64
	if viewer >= 0 && viewer < MaxSeats && g.Players[viewer] != nil {
		vx, vy = g.Players[viewer].X, g.Players[viewer].Y
	}
	close := func(x, y float64) bool {
		dx, dy := x-vx, y-vy
		return dx*dx+dy*dy < ViewR*ViewR
	}
	pls := make([]any, MaxSeats)
	for i := range MaxSeats {
		p := g.Players[i]
		if p == nil {
			pls[i] = map[string]any{"x": 0, "y": 0, "hp": 0, "maxhp": 0,
				"level": 0, "xp": 0, "xpn": 0, "down": false, "rev": 0,
				"bleed": 0, "dead": false, "wpn": map[string]any{},
				"arti": []any{}, "evo": []any{}, "blade": 0, "frost": 0,
				"pending": nil}
			continue
		}
		wpn := map[string]any{}
		for k, v := range p.Wpn {
			wpn[k] = v
		}
		var pending any
		if p.Pending != nil {
			pending = p.Pending
		}
		pls[i] = map[string]any{
			"x": r1(p.X), "y": r1(p.Y), "hp": r1(p.HP), "maxhp": p.MaxHP,
			"level": p.Level, "xp": p.XP, "xpn": p.XPN,
			"down": p.Down, "rev": math.Round(p.Revive*100) / 100,
			"bleed": r1(p.Bleed), "dead": p.Dead,
			"wpn": wpn, "arti": p.Arti, "evo": p.Evo,
			"blade": math.Round(p.BladeA*1000) / 1000,
			"frost": math.Round(p.FrostA*1000) / 1000,
			"pending": pending,
		}
	}
	enemies := []any{}
	for _, e := range g.Enemies {
		if !close(e.X, e.Y) {
			continue
		}
		frac := 0.0
		if e.MaxHP > 0 {
			frac = math.Max(0, e.HP/e.MaxHP)
		}
		enemies = append(enemies, []any{r1(e.X), r1(e.Y),
			math.Round(frac*100) / 100, kindCode[e.Kind],
			boolInt(e.Elite), e.R, e.Tier, modeCode[e.Mode]})
	}
	shots := []any{}
	for _, sh := range g.Shots {
		if !close(sh.X, sh.Y) {
			continue
		}
		shots = append(shots, []any{r1(sh.X), r1(sh.Y), r1(sh.VX), r1(sh.VY), boolInt(sh.Aoe != 0)})
	}
	axes := []any{}
	for _, a := range g.Axes {
		axes = append(axes, []any{r1(a.X), r1(a.Y), r1(a.VX), r1(a.VY)})
	}
	acids := []any{}
	for _, a := range g.Acids {
		acids = append(acids, []any{r1(a.X), r1(a.Y), r1(a.R),
			math.Round(math.Max(0, a.Until-now)*10) / 10})
	}
	fx := []any{}
	for _, f := range g.Fx {
		fx = append(fx, map[string]any{"kind": f.Kind, "pts": f.Pts})
	}
	gems := []any{}
	for _, gm := range g.Gems {
		if !close(gm.X, gm.Y) {
			continue
		}
		gems = append(gems, []any{r1(gm.X), r1(gm.Y), gm.V})
	}
	var boss any
	if g.Boss != nil {
		frac := math.Max(0, g.Boss.HP/g.Boss.MaxHP)
		boss = []any{r1(g.Boss.X), r1(g.Boss.Y),
			math.Round(frac*1000) / 1000,
			math.Round(math.Max(0, g.Boss.HP)*10) / 10,
			math.Round(g.Boss.MaxHP*10) / 10}
	}
	names := make([]any, MaxSeats)
	ready := make([]any, MaxSeats)
	connected := make([]any, MaxSeats)
	for i := range MaxSeats {
		if p := g.Players[i]; p != nil {
			names[i] = p.Name
			ready[i] = p.Ready
			connected[i] = true
		} else {
			names[i] = ""
			ready[i] = false
			connected[i] = false
		}
	}
	return map[string]any{
		"v": VERSION,
		"phase": g.Phase,
		"countdown": cd,
		"time": math.Round(g.Time*10) / 10,
		"kills": g.Kills,
		"players": pls,
		"enemies": enemies,
		"shots": shots,
		"axes": axes,
		"acid": acids,
		"fx": fx,
		"gems": gems,
		"boss": boss,
		"names": names,
		"ready": ready,
		"connected": connected,
		"last_lvl": g.LastLvl,
		"last_moon": g.LastMoon,
		"blood": math.Round(math.Max(0, g.BloodUntil-now)*10) / 10,
		"last_hurt": g.LastHurt,
		"last_goblin": g.LastGoblin,
		"last_boss": g.LastBoss,
		"last_over": g.LastOver,
		"paused": g.Paused,
		"paused_by": g.PausedBy,
		"event_id": g.EventID,
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
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
		out := s.snapshot(s.snapViewer(pid), now)
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
				for i := range MaxSeats {
					if g.Players[i] == nil {
						sl = i
						break
					}
				}
				if sl < 0 {
					writeJSON(w, map[string]any{"you": 0}, 200)
					return
				}
				nb := Mkbuild()
				nb.ID, nb.Name = pid, name
				nb.LastSeen = now
				nb.IX, nb.IY = 0, 0
				nb.X, nb.Y = float64(sl%2)*400-200, float64(sl/2)*400-200
				if g.Phase == "playing" {
					nb.Prot = now + 3
				}
				g.Players[sl] = nb
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
		case hasSuffix(path, "/input") && sl >= 0:
			ix, iy := 0.0, 0.0
			if v, ok := body["x"].(float64); ok {
				ix = math.Max(-1, math.Min(1, v))
			}
			if v, ok := body["y"].(float64); ok {
				iy = math.Max(-1, math.Min(1, v))
			}
			g.Players[sl].IX, g.Players[sl].IY = ix, iy
			writeJSON(w, map[string]any{"ok": true}, 200)
		case hasSuffix(path, "/pick") && sl >= 0:
			b := g.Players[sl]
			if b.Pending != nil {
				var opts []string
				for _, o := range b.Pending {
					opts = append(opts, o.ID)
				}
				oid := sstr("id")
				found := false
				for _, o := range opts {
					if o == oid {
						found = true
						break
					}
				}
				if found {
					g.ApplyPick(sl, oid)
					b.Pending = nil
					writeJSON(w, map[string]any{"ok": true}, 200)
					break
				}
			}
			writeJSON(w, map[string]any{"ok": false}, 200)
		case hasSuffix(path, "/ready") && sl >= 0:
			g.Players[sl].Ready = true
			writeJSON(w, map[string]any{"ok": true}, 200)
		case hasSuffix(path, "/input"), hasSuffix(path, "/pick"), hasSuffix(path, "/ready"):
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
				for _, p := range g.Players {
					if p != nil {
						p.Ready = false
					}
				}
				g.Phase = "waiting"
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

	fmt.Printf("\n  NEON HORDE running! (go build) 2-4 hunters, endless infinite field.\n  On this machine:  http://localhost:%d\n", port)
	for _, ip := range lanIPs() {
		fmt.Printf("  Friend on same WiFi:  http://%s:%d\n", ip, port)
	}
	fmt.Println("\n  Survive. Revive. Draft. Boss every 4 minutes.")
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
