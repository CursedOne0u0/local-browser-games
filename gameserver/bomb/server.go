package bomb

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type runnerJSON struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Score  float64 `json:"score"`
	Out    bool    `json:"out"`
	Dash   float64 `json:"dash"`
	DashCD float64 `json:"dash_cd"`
	Boost  float64 `json:"boost"`
	Imm    float64 `json:"imm"`
}
type padJSON struct {
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	DX  float64 `json:"dx"`
	DY  float64 `json:"dy"`
	TTL float64 `json:"ttl"`
}
type pillarJSON struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

func (g *gameT) snapshot() map[string]any {
	cd := 0
	if g.phase == "countdown" {
		c := math.Ceil(time.Until(g.countEnd).Seconds())
		if c > 0 {
			cd = int(c)
		} else {
			cd = 1
		}
	}
	t := time.Now()
	rs := make([]runnerJSON, 0, MaxSeats)
	for i := 0; i < MaxSeats; i++ {
		rs = append(rs, runnerJSON{
			round1(g.pos[i].X), round1(g.pos[i].Y), g.pos[i].Score, g.out[i],
			fsec(g.dashUntil[i].Sub(t)), fsec(g.dashCD[i].Sub(t)),
			fsec(g.boostUntil[i].Sub(t)), fsec(g.immUntil[i].Sub(t)),
		})
	}
	pds := []padJSON{}
	for _, pd := range g.pads {
		pds = append(pds, padJSON{round1(pd.X), round1(pd.Y), round3(pd.DX), round3(pd.DY), fsec(pd.Expires.Sub(t))})
	}
	pls := []pillarJSON{}
	for _, pl := range g.pillars {
		pls = append(pls, pillarJSON{pl.X, pl.Y, pl.W, pl.H})
	}
	names := make([]string, 0, MaxSeats)
	ready := make([]bool, 0, MaxSeats)
	conn := make([]bool, 0, MaxSeats)
	for i := 0; i < MaxSeats; i++ {
		if g.players[i] != nil {
			names = append(names, g.players[i].Name)
			ready = append(ready, g.players[i].Ready)
			conn = append(conn, true)
		} else {
			names = append(names, "")
			ready = append(ready, false)
			conn = append(conn, false)
		}
	}
	holder := 0
	if g.phase == "playing" || g.phase == "round" {
		holder = g.holder
	}
	return map[string]any{
		"v": Version, "phase": g.phase, "countdown": cd, "round": g.round,
		"aw": g.aw, "ah": g.ah, "pillars": pls, "runners": rs, "pads": pds,
		"holder": holder, "fuse": math.Round(math.Max(0, g.fuse)*100) / 100,
		"winner": g.winner, "loser": g.loser, "blasts": g.blasts,
		"names": names, "ready": ready, "connected": conn,
		"last_pass": g.lastPass, "last_boom": g.lastBoom, "last_boost": g.lastBoost,
		"paused": g.paused, "paused_by": g.pausedBy,
		"event_id": g.eventID,
	}
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
	// repo layout: gameserver/bin/<bin> -> ../../local-browser-games/2players/bomb-tag/public
	dir := filepath.Join(filepath.Dir(exe), "..", "..", "local-browser-games", "2players", "bomb-tag", "public")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir
	}
	return "public"
}

func Run(port int) {
	g := newGame()
	g.applyArena(2)
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(publicDir())))
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		pid := r.URL.Query().Get("id")
		g.mu.Lock()
		g.touch(pid)
		s := g.snapshot()
		g.mu.Unlock()
		writeJSON(w, s, 200)
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
		g.mu.Lock()
		defer g.mu.Unlock()
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/join"):
			name := sstr("name")
			if len(name) > 32 {
				name = name[:32]
			}
			if name == "" {
				name = "Player"
			}
			s := g.slotOf(pid)
			if s < 0 {
				g.freeStale()
				s = -1
				for i := 0; i < MaxSeats; i++ {
					if g.players[i] == nil {
						s = i
						break
					}
				}
				if s < 0 {
					writeJSON(w, map[string]any{"you": 0}, 200)
					return
				}
				g.players[s] = &player{ID: pid, Name: name, LastSeen: time.Now(), Ready: false}
				g.out[s] = false
				if g.phase == "countdown" || g.phase == "playing" || g.phase == "round" {
					occ := g.occupied()
					sc := []float64{0}
					for _, i := range occ {
						if i != s {
							sc = append(sc, g.pos[i].Score)
						}
					}
					floor := sc[0]
					for _, v := range sc[1:] {
						if len(occ)+1 >= 3 {
							if v > floor {
								floor = v
							}
						} else if v < floor {
							floor = v
						}
					}
					sps := g.spawns
					if len(sps) == 0 {
						sps = []spawn{{100, 280}}
					}
					sp := sps[s%len(sps)]
					g.pos[s] = pos{sp.X, sp.Y, floor}
				}
			} else {
				g.players[s].Name = name
				g.players[s].LastSeen = time.Now()
			}
			writeJSON(w, map[string]any{"you": s + 1}, 200)
			return
		}
		g.touch(pid)
		s := g.slotOf(pid)
		num := func(k string) float64 {
			if v, ok := body[k].(float64); ok {
				return v
			}
			return 0
		}
		switch {
		case strings.HasSuffix(path, "/input") && s >= 0:
			ix := math.Max(-1, math.Min(1, num("x")))
			iy := math.Max(-1, math.Min(1, num("y")))
			pl := g.players[s]
			pl.IX, pl.IY = ix, iy
			if f, ok := body["fire"].(bool); ok {
				pl.Fire = f
			} else {
				pl.Fire = num("fire") != 0
			}
			writeJSON(w, map[string]any{"ok": true}, 200)
		case strings.HasSuffix(path, "/ready") && s >= 0:
			g.players[s].Ready = true
			writeJSON(w, map[string]any{"ok": true}, 200)
		case strings.HasSuffix(path, "/leave"):
			if s >= 0 {
				g.players[s] = nil
			}
			writeJSON(w, map[string]any{"ok": true}, 200)
		case strings.HasSuffix(path, "/pause"):
			if g.paused {
				g.shiftPaused(time.Since(g.pausedSince))
				g.paused = false
				g.pausedBy = ""
			} else {
				var nm string
				if s >= 0 && g.players[s] != nil {
					nm = g.players[s].Name
				}
				if nm == "" {
					nm = "Someone"
				}
				g.paused = true
				g.pausedBy = nm
				g.pausedSince = time.Now()
			}
			writeJSON(w, map[string]any{"ok": true, "paused": g.paused, "by": g.pausedBy}, 200)
		case strings.HasSuffix(path, "/restart"):
			if g.phase == "over" {
				for _, p := range g.players {
					if p != nil {
						p.Ready = false
					}
				}
				g.phase = "waiting"
				g.winner, g.loser, g.round = 0, 0, 1
			}
			writeJSON(w, map[string]any{"ok": true}, 200)
		default:
			writeJSON(w, map[string]any{"ok": false}, 400)
		}
	})

	go func() {
		last := time.Now()
		for {
			t := time.Now()
			dt := t.Sub(last).Seconds()
			if dt > 0.05 {
				dt = 0.05
			}
			last = t
			g.mu.Lock()
			g.freeStale()
			if !g.paused {
				occ := g.occupied()
				allReady := len(occ) >= 2
				if allReady {
					for _, i := range occ {
						if !g.players[i].Ready {
							allReady = false
							break
						}
					}
				}
				if g.phase == "waiting" && allReady {
					g.phase = "countdown"
					g.countEnd = time.Now().Add(2400 * time.Millisecond)
					g.nstart = len(occ)
					g.applyArena(len(occ))
					g.resetScores()
					g.newRound()
					g.lastPass, g.lastBoom = nil, nil
				}
				g.step(dt)
			}
			g.mu.Unlock()
			time.Sleep(time.Second / 60)
		}
	}()

	fmt.Printf("\n  BOMB TAG running! (go build)\n  On this laptop:  http://localhost:%d\n", port)
	for _, ip := range lanIPs() {
		fmt.Printf("  Friend on same WiFi:  http://%s:%d\n", ip, port)
	}
	fmt.Println("\n  WASD/arrows to run, SPACE to dash. Holder is slower — pass it!")
	srv := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", port), Handler: mux}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Println("server error:", err)
		os.Exit(1)
	}
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
