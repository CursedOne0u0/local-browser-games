// Bastion Bros LAN server (Go port of 2players/bastion/server.py).
// Routes and JSON are identical to the Python server.
package bastion

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
	// repo layout: gameserver/bin/<bin> -> ../../local-browser-games/2players/bastion/public
	dir := filepath.Join(filepath.Dir(exe), "..", "..", "local-browser-games",
		"2players", "bastion", "public")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir
	}
	return "public"
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t != ""
	default:
		return v != nil
	}
}

func (s *Server) snapshot(now float64) map[string]any {
	g := s.sim
	cd := 0
	if g.Phase == "countdown" {
		cd = int(math.Max(1, math.Ceil(g.CountdownEnd-now)))
	}
	grid := []any{}
	for _, t := range g.Grid {
		grid = append(grid, map[string]any{"c": t.C, "r": t.R, "type": t.Type,
			"hp": r1(t.HP), "max": Towers[t.Type].HP})
	}
	enemies := []any{}
	for _, e := range g.Enemies {
		enemies = append(enemies, map[string]any{"x": r1(e.X), "y": r1(e.Y),
			"hp": r1(math.Max(0, e.HP)), "maxhp": e.MaxHP, "kind": e.Kind})
	}
	players := make([]any, MaxSeats)
	for i := range MaxSeats {
		if p := g.Players[i]; p != nil {
			players[i] = map[string]any{"x": r1(p.X), "y": r1(p.Y), "sel": p.Sel}
		} else {
			players[i] = nil
		}
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
		"level": g.Level, "wave": g.Wave, "wavesTotal": g.Level + 2,
		"gold": g.Gold, "base": g.Base,
		"grid": grid,
		"enemies": enemies,
		"players": players,
		"names": names,
		"ready": ready,
		"connected": connected,
		"last_wave": g.LastWave,
		"last_over": g.LastOver,
		"last_build": g.LastBuild,
		"paused": g.Paused,
		"paused_by": g.PausedBy,
		"build_cd": math.Round(math.Max(0, g.BuildEnd-now)*10) / 10,
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
				g.Players[sl] = &Player{ID: pid, Name: name, LastSeen: now,
					X: 120 + float64(sl)*120, Y: 540}
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
			if v, ok := body["sel"].(float64); ok {
				n := int(v) % 3
				if n < 0 {
					n += 3
				}
				g.Players[sl].Sel = n
			}
			if truthy(body["build"]) {
				g.Players[sl].Build = true
			}
			writeJSON(w, map[string]any{"ok": true}, 200)
		case hasSuffix(path, "/wave") && sl >= 0:
			writeJSON(w, map[string]any{"ok": g.StartWave()}, 200)
		case hasSuffix(path, "/ready") && sl >= 0:
			g.Players[sl].Ready = true
			writeJSON(w, map[string]any{"ok": true}, 200)
		case hasSuffix(path, "/input"), hasSuffix(path, "/wave"), hasSuffix(path, "/ready"):
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

	fmt.Printf("\n  BASTION LAN running! (go build) 2-4 defenders hold the road.\n  On this machine:  http://localhost:%d\n", port)
	for _, ip := range lanIPs() {
		fmt.Printf("  Friend on same WiFi:  http://%s:%d\n", ip, port)
	}
	fmt.Println("\n  Stand on grass + build key. Mid-wave builds +25%.")
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
