// Echo Hunt LAN server (Go port of 2players/echo-hunt/server.py).
// Routes and JSON are identical to the Python server.
package echo

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
	// repo layout: gameserver/bin/<bin> -> ../../local-browser-games/2players/echo-hunt/public
	dir := filepath.Join(filepath.Dir(exe), "..", "..", "local-browser-games",
		"2players", "echo-hunt", "public")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir
	}
	return "public"
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- snapshot (role-dependent, like server.py snapshot(pid)) ---

func (s *Server) snapshot(pid string, now float64) map[string]any {
	g := s.sim
	me := g.SlotOf(pid)
	role := ""
	if me >= 0 {
		role = g.Roles[me]
	}
	var nodesOut []any
	if role != "hunter" {
		nodesOut = []any{}
		for _, nd := range g.Nodes {
			busy := false
			for _, sl := range nd.Slots {
				if sl.Solver != -1 {
					busy = true
					break
				}
			}
			nodesOut = append(nodesOut, map[string]any{
				"x": r1(nd.X), "y": r1(nd.Y), "done": nd.Done, "busy": busy})
		}
	} else {
		nodesOut = []any{}
	}
	var myslot any
	if me >= 0 {
	outer:
		for ni, nd := range g.Nodes {
			for si, sl := range nd.Slots {
				if sl.Solver == me {
					myslot = map[string]any{"node": ni, "slot": si, "n": sl.N,
						"src": sl.Src, "dst": sl.Dst}
					break outer
				}
			}
		}
	}
	cd := 0
	if g.Phase == "countdown" {
		cd = int(math.Max(1, math.Ceil(g.CountdownEnd-now)))
	}
	timeleft := 0.0
	if g.Phase == "playing" {
		timeleft = math.Round(math.Max(0, g.RoundTimeEnd-now)*10) / 10
	}
	youSlot := 0
	if me >= 0 {
		youSlot = me + 1
	}
	pillars := make([]any, len(Pillars))
	for i, p := range Pillars {
		pillars[i] = map[string]any{"x": p.X, "y": p.Y, "w": p.Wd, "h": p.Ht}
	}
	runners := make([]any, MaxPlayers)
	for i := range MaxPlayers {
		r := g.Runners[i]
		runners[i] = map[string]any{"x": r1(r.X), "y": r1(r.Y),
			"alive": r.Alive, "stam": math.Round(r.Stam*100) / 100}
	}
	rings := []any{}
	for _, rg := range g.Rings {
		rings = append(rings, map[string]any{"x": rg.X, "y": rg.Y,
			"age": math.Round((now-rg.Born)*100) / 100})
	}
	blips := []any{}
	for _, b := range g.Blips {
		if now < b.Until {
			blips = append(blips, map[string]any{"x": b.X, "y": b.Y,
				"str": b.Str, "left": math.Round((b.Until-now)*100) / 100})
		}
	}
	names := make([]any, MaxPlayers)
	ready := make([]any, MaxPlayers)
	connected := make([]any, MaxPlayers)
	roles := make([]any, MaxPlayers)
	for i := range MaxPlayers {
		if p := g.Players[i]; p != nil {
			names[i] = p.Name
			ready[i] = p.Ready
			connected[i] = true
		} else {
			names[i] = ""
			ready[i] = false
			connected[i] = false
		}
		if g.Roles[i] != "" {
			roles[i] = g.Roles[i]
		} else {
			roles[i] = nil
		}
	}
	host := g.IPs[pid] == "127.0.0.1" || g.IPs[pid] == "::1"
	return map[string]any{
		"v": VERSION,
		"phase": g.Phase,
		"countdown": cd,
		"round": g.Round,
		"h_wins": g.HWins, "d_wins": g.DWins,
		"winner_side": g.WinnerSide,
		"paused": g.Paused,
		"paused_by": g.PausedBy,
		"timeleft": timeleft,
		"you": map[string]any{"slot": youSlot, "role": role},
		"roles": roles,
		"pillars": pillars,
		"runners": runners,
		"nodes": nodesOut,
		"myslot": myslot,
		"target": g.Target, "cracked": g.Cracked,
		"rings": rings,
		"blips": blips,
		"names": names,
		"ready": ready,
		"connected": connected,
		"last_ping": g.LastPing,
		"last_shout": g.LastShout,
		"last_tag": g.LastTag,
		"last_collect": g.LastCollect,
		"last_round": g.LastRound,
		"event_id": g.EventID,
		"host": host,
	}
}

func r1(v float64) float64 { return math.Round(v*10) / 10 }

func Run(port int) {
	s := &Server{sim: New()}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(publicDir())))
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		pid := r.URL.Query().Get("id")
		ip := clientIP(r)
		now := unixNow()
		s.mu.Lock()
		if pid != "" {
			s.sim.IPs[pid] = ip
		}
		s.sim.Touch(pid, now)
		out := s.snapshot(pid, now)
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
		ip := clientIP(r)
		now := unixNow()
		s.mu.Lock()
		defer s.mu.Unlock()
		g := s.sim
		if pid != "" {
			g.IPs[pid] = ip
		}
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
				for i := range MaxPlayers {
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
		switch {
		case hasSuffix(path, "/input") && sl >= 0:
			ix, iy := num("x"), num("y")
			if _, ok := body["x"].(float64); !ok {
				ix = 0
			}
			if _, ok := body["y"].(float64); !ok {
				iy = 0
			}
			ix = math.Max(-1, math.Min(1, ix))
			iy = math.Max(-1, math.Min(1, iy))
			if n := math.Hypot(ix, iy); n > 1 {
				ix /= n
				iy /= n
			}
			g.Players[sl].IX, g.Players[sl].IY = ix, iy
			writeJSON(w, map[string]any{"ok": true}, 200)
		case hasSuffix(path, "/ready") && sl >= 0:
			g.Players[sl].Ready = true
			writeJSON(w, map[string]any{"ok": true}, 200)
		case hasSuffix(path, "/leave"):
			if sl >= 0 {
				g.ReleaseSlots(sl)
				g.Players[sl] = nil
				g.Roles[sl] = ""
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
		case hasSuffix(path, "/node_done") && sl >= 0:
			var lv any
			if body["links"] != nil {
				lv = body["links"]
			} else {
				lv = []any{}
			}
			links, ok := ParseLinks(lv)
			writeJSON(w, g.SolveAttempt(sl, links, ok, now), 200)
		case hasSuffix(path, "/node_abandon") && sl >= 0:
			g.ReleaseSlots(sl)
			writeJSON(w, map[string]any{"ok": true}, 200)
		case hasSuffix(path, "/input"), hasSuffix(path, "/ready"),
			hasSuffix(path, "/node_done"), hasSuffix(path, "/node_abandon"):
			writeJSON(w, map[string]any{"ok": false}, 400)
		case hasSuffix(path, "/bot"):
			if ip != "127.0.0.1" && ip != "::1" {
				writeJSON(w, map[string]any{"ok": false, "err": "host only"}, 200)
				break
			}
			n := 0
			switch v := body["n"].(type) {
			case float64:
				n = int(v)
			case string:
				var err error
				n, err = atoiTrim(v)
				if err != nil {
					n = 0
				}
			}
			writeJSON(w, map[string]any{"ok": true, "bots": g.SetBots(n, now)}, 200)
		case hasSuffix(path, "/restart"):
			if g.Phase == "over" {
				g.ResetMatch()
				g.AssignRoles(now)
				for _, p := range g.Players {
					if p != nil && !p.Bot {
						p.Ready = false
					}
				}
				g.Phase = "waiting"
				g.WinnerSide = 0
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
				if g.ShouldStart() {
					g.EnterCountdown(now)
				}
				g.Step(dt, now)
			}
			s.mu.Unlock()
			time.Sleep(time.Second / 60)
		}
	}()

	fmt.Printf("\n  ECHO HUNT running! (go build)\n  On this machine:  http://localhost:%d\n", port)
	for _, ip := range lanIPs() {
		fmt.Printf("  Friend on same WiFi:  http://%s:%d\n", ip, port)
	}
	fmt.Println("\n  2-8 players, one phone each. Wardens hunt blind on sonar;\n  divers crack wire nodes. First side to 3 rounds wins!")
	srv := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", port), Handler: mux}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Println("server error:", err)
		os.Exit(1)
	}
}

func atoiTrim(v string) (int, error) {
	t := v
	for len(t) > 0 && (t[0] == ' ' || t[0] == '\t' || t[0] == '\n') {
		t = t[1:]
	}
	for len(t) > 0 && (t[len(t)-1] == ' ' || t[len(t)-1] == '\t' || t[len(t)-1] == '\n') {
		t = t[:len(t)-1]
	}
	n := 0
	neg := false
	if len(t) > 0 && (t[0] == '-' || t[0] == '+') {
		neg = t[0] == '-'
		t = t[1:]
	}
	if t == "" {
		return 0, fmt.Errorf("bad int")
	}
	for i := 0; i < len(t); i++ {
		if t[i] < '0' || t[i] > '9' {
			return 0, fmt.Errorf("bad int")
		}
		n = n*10 + int(t[i]-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
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
