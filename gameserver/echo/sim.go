// Package echo is a Go port of the Echo Hunt LAN server (2players/echo-hunt).
// Wire protocol and game logic are identical to server.py.
package echo

import (
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"
)

const VERSION = "1.17"

const (
	MaxPlayers      = 8
	WinRounds       = 3
	W               = 2200.0
	H               = 1400.0
	RunR            = 15.0
	DiverSpeed      = 300.0
	HunterSpeed     = 270.0
	TagDist         = 34.0
	NodeR           = 26.0
	ChannelTime     = 2.0
	LiveNodes       = 3
	SpawnClearHunter = 350.0
	NodeSpread      = 300.0
	RoundTime       = 180.0
	PingEvery       = 4.0
	BotSolveMin     = 5.0
	BotSolveMax     = 9.0
)

var BotNames = []string{"Byte", "Echo", "Sonar", "Pixel", "Glitch", "Watt", "Ping", "Fathom"}
var PickColors = []string{"red", "orange", "yellow", "green", "cyan", "blue", "purple", "pink"}

type Pillar struct {
	X, Y, Wd, Ht float64
}

var Pillars = []Pillar{
	{300, 250, 80, 80},
	{1820, 1070, 80, 80},
	{700, 900, 120, 60},
	{1380, 440, 120, 60},
	{1050, 200, 60, 140},
	{1090, 1060, 60, 140},
	{1500, 700, 100, 100},
	{600, 600, 100, 100},
	{350, 1050, 140, 70},
	{1710, 280, 140, 70},
	{950, 600, 70, 200},
	{1180, 600, 70, 200},
	{1800, 600, 80, 160},
	{320, 640, 80, 160},
}

var DiverSpawns = [][2]float64{{150, 150}, {2050, 150}, {150, 1250}, {2050, 1250},
	{150, 700}, {2050, 700}, {1100, 150}, {1100, 1250}}
var HunterSpawns = [][2]float64{{1100, 700}, {1070, 660}, {1130, 660}, {1070, 740}, {1130, 740}}

type Player struct {
	ID       string
	Name     string
	LastSeen float64
	Ready    bool
	IX, IY   float64
	Bot      bool
}

type Runner struct {
	X, Y   float64
	Alive  bool
	Stam   float64
	Gassed bool
}

type Scan struct {
	Node  int
	Since float64
}

type Slot struct {
	Solver   int // seat, -1 = none
	Resolved bool
	N        int
	Src      []string
	Dst      []string
	Perm     []int
}

type Node struct {
	X, Y  float64
	Done  int
	Slots []*Slot
}

type Ring struct {
	X, Y float64
	Born float64
}

type Blip struct {
	X, Y  float64
	Str   float64
	Until float64
}

type BotState struct {
	SolveAt float64
	HasSol  bool
	WpX, WpY float64
	HasWp   bool
	WpAt    float64
}

type PingEv struct {
	ID int `json:"id"`
}
type ShoutEv struct {
	X, Y float64
	By   string `json:"by"`
	ID   int    `json:"id"`
}
type TagEv struct {
	Hunter int `json:"hunter"`
	Victim int `json:"victim"`
	ID     int `json:"id"`
}
type CollectEv struct {
	By     string `json:"by"`
	Done   int    `json:"done"`
	Target int    `json:"target"`
	ID     int    `json:"id"`
}
type RoundEv struct {
	Side   int    `json:"side"`
	Reason string `json:"reason"`
	ID     int    `json:"id"`
}

type Sim struct {
	Phase        string
	CountdownEnd float64
	RoundEnd     float64
	Round        int
	HWins, DWins int
	WinnerSide   int
	Players      [MaxPlayers]*Player
	Bots         map[int]*BotState
	IPs          map[string]string
	Runners      [MaxPlayers]*Runner
	EffMag       [MaxPlayers]float64
	Roles        [MaxPlayers]string // "" | "hunter" | "diver"
	HuntCounts   map[string]int
	Channel      [MaxPlayers]*Scan
	Nodes        []*Node
	Target       int
	Cracked      int
	Rings        []Ring
	Blips        []Blip
	NextPingAt   float64
	Paused       bool
	PausedBy     string
	PausedSince  float64
	RoundTimeEnd float64
	EventID      int
	LastPing     *PingEv
	LastShout    *ShoutEv
	LastTag      *TagEv
	LastCollect  *CollectEv
	LastRound    *RoundEv
	rng          *rand.Rand
}

func New() *Sim {
	s := &Sim{
		Phase: "waiting", Round: 1,
		Bots: map[int]*BotState{}, IPs: map[string]string{},
		HuntCounts: map[string]int{},
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	for i := range MaxPlayers {
		s.Runners[i] = &Runner{Alive: true, Stam: 1.0}
	}
	return s
}

func unixNow() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// --- presence ---

func (s *Sim) Touch(pid string, now float64) {
	for _, p := range s.Players {
		if p != nil && p.ID == pid {
			p.LastSeen = now
		}
	}
}

func (s *Sim) SlotOf(pid string) int {
	for i, p := range s.Players {
		if p != nil && p.ID == pid {
			return i
		}
	}
	return -1
}

func (s *Sim) FreeStale(now float64) {
	for i, p := range s.Players {
		if p != nil && !p.Bot && now-p.LastSeen > 8 {
			s.ReleaseSlots(i)
			s.Players[i] = nil
			s.Roles[i] = ""
			s.Channel[i] = nil
		}
	}
}

// --- world ---

func Collide(x, y float64) (float64, float64) {
	x = math.Max(RunR, math.Min(W-RunR, x))
	y = math.Max(RunR, math.Min(H-RunR, y))
	for _, pl := range Pillars {
		cx := math.Max(pl.X, math.Min(pl.X+pl.Wd, x))
		cy := math.Max(pl.Y, math.Min(pl.Y+pl.Ht, y))
		dx, dy := x-cx, y-cy
		d2 := dx*dx + dy*dy
		if d2 < RunR*RunR {
			if d2 > 1e-6 {
				d := math.Sqrt(d2)
				x = cx + dx/d*RunR
				y = cy + dy/d*RunR
			} else {
				l, r, tp, b := x-pl.X, pl.X+pl.Wd-x, y-pl.Y, pl.Y+pl.Ht-y
				m := math.Min(math.Min(l, r), math.Min(tp, b))
				switch m {
				case l:
					x = pl.X - RunR
				case r:
					x = pl.X + pl.Wd + RunR
				case tp:
					y = pl.Y - RunR
				default:
					y = pl.Y + pl.Ht + RunR
				}
			}
		}
	}
	return x, y
}

func PointClear(x, y float64, margin float64) bool {
	if !(margin < x && x < W-margin && margin < y && y < H-margin) {
		return false
	}
	for _, pl := range Pillars {
		if pl.X-margin < x && x < pl.X+pl.Wd+margin &&
			pl.Y-margin < y && y < pl.Y+pl.Ht+margin {
			return false
		}
	}
	return true
}

func (s *Sim) Hunters() []int {
	var out []int
	for i, r := range s.Roles {
		if r == "hunter" {
			out = append(out, i)
		}
	}
	return out
}

func (s *Sim) Divers() []int {
	var out []int
	for i, r := range s.Roles {
		if r == "diver" {
			out = append(out, i)
		}
	}
	return out
}

func (s *Sim) ReleaseSlots(seat int) {
	for _, nd := range s.Nodes {
		for _, sl := range nd.Slots {
			if sl.Solver == seat {
				sl.Solver = -1
			}
		}
	}
	s.Channel[seat] = nil
}

func (s *Sim) HoldsSlot(seat int) bool {
	for _, nd := range s.Nodes {
		for _, sl := range nd.Slots {
			if sl.Solver == seat {
				return true
			}
		}
	}
	return false
}

func (s *Sim) MakePuzzle() *Slot {
	k := 3 + s.rng.Intn(6)
	cols := append([]string{}, PickColors...)
	s.rng.Shuffle(len(cols), func(a, b int) { cols[a], cols[b] = cols[b], cols[a] })
	cols = cols[:k]
	perm := s.rng.Perm(k)
	dst := make([]string, k)
	for d := range k {
		dst[d] = cols[perm[d]]
	}
	return &Slot{Solver: -1, N: k, Src: append([]string{}, cols...), Dst: dst, Perm: perm}
}

func (s *Sim) SpawnNode() {
	var hx, hy []float64
	for _, i := range s.Hunters() {
		hx = append(hx, s.Runners[i].X)
		hy = append(hy, s.Runners[i].Y)
	}
	for range 120 {
		x := 70 + s.rng.Float64()*(W-140)
		y := 70 + s.rng.Float64()*(H-140)
		if !PointClear(x, y, 30) {
			continue
		}
		if len(hx) > 0 {
			best := math.MaxFloat64
			for k := range hx {
				if d := math.Hypot(x-hx[k], y-hy[k]); d < best {
					best = d
				}
			}
			if best < SpawnClearHunter {
				continue
			}
		}
		spread := true
		for _, nd := range s.Nodes {
			if math.Hypot(x-nd.X, y-nd.Y) < NodeSpread {
				spread = false
				break
			}
		}
		if !spread {
			continue
		}
		s.Nodes = append(s.Nodes, &Node{X: x, Y: y,
			Slots: []*Slot{s.MakePuzzle(), s.MakePuzzle(), s.MakePuzzle()}})
		return
	}
}

func (s *Sim) ResetPositions() {
	hi, di := 0, 0
	for i := range MaxPlayers {
		r := s.Runners[i]
		switch s.Roles[i] {
		case "hunter":
			sp := HunterSpawns[hi%len(HunterSpawns)]
			hi++
			r.X, r.Y = sp[0], sp[1]
		case "diver":
			sp := DiverSpawns[di%len(DiverSpawns)]
			di++
			r.X, r.Y = sp[0], sp[1]
		default:
			continue
		}
		r.Alive = true
		r.Stam = 1.0
		r.Gassed = false
		s.Channel[i] = nil
		s.EffMag[i] = 0
	}
}

func (s *Sim) AssignRoles(now float64) {
	_ = now
	var seated []int
	for i, p := range s.Players {
		if p != nil {
			seated = append(seated, i)
		}
	}
	n := len(seated)
	nh := int(math.Round(float64(n) / 3))
	if nh < 1 {
		nh = 1
	}
	type cand struct {
		seat int
		hc   int
		roll float64
	}
	cands := make([]cand, len(seated))
	for k, i := range seated {
		cands[k] = cand{i, s.HuntCounts[s.Players[i].ID], s.rng.Float64()}
	}
	sort.Slice(cands, func(a, b int) bool {
		if cands[a].hc != cands[b].hc {
			return cands[a].hc < cands[b].hc
		}
		return cands[a].roll < cands[b].roll
	})
	for i := range MaxPlayers {
		s.Roles[i] = ""
	}
	for k, c := range cands {
		if k < nh {
			s.Roles[c.seat] = "hunter"
			pid := s.Players[c.seat].ID
			s.HuntCounts[pid]++
		} else {
			s.Roles[c.seat] = "diver"
		}
	}
	s.Target = 4 + (len(cands) - nh)
	s.Cracked = 0
	s.Nodes = nil
	s.Rings = nil
	s.Blips = nil
	s.ResetPositions()
	for range LiveNodes {
		s.SpawnNode()
	}
}

func (s *Sim) ResetMatch() {
	s.HWins, s.DWins = 0, 0
	s.WinnerSide = 0
	s.Round = 1
	s.HuntCounts = map[string]int{}
	s.LastTag, s.LastCollect, s.LastRound, s.LastShout = nil, nil, nil, nil
}

func (s *Sim) Shout(x, y float64, by string, now float64) {
	s.EventID++
	s.LastShout = &ShoutEv{X: math.Round(x*10) / 10, Y: math.Round(y*10) / 10, By: by, ID: s.EventID}
	s.Blips = append(s.Blips, Blip{X: x, Y: y, Str: 1.2, Until: now + 2.5})
}

func (s *Sim) WinRound(side int, reason string, now float64) {
	_ = now
	if side == 1 {
		s.HWins++
	} else {
		s.DWins++
	}
	s.EventID++
	s.LastRound = &RoundEv{Side: side, Reason: reason, ID: s.EventID}
	if s.HWins >= WinRounds || s.DWins >= WinRounds {
		s.Phase = "over"
		if s.HWins >= WinRounds {
			s.WinnerSide = 1
		} else {
			s.WinnerSide = 2
		}
	} else {
		s.Phase = "round"
		s.RoundEnd = now + 2.5
	}
}

func (s *Sim) SonarPing(now float64) {
	s.NextPingAt = now + PingEvery
	for _, i := range s.Hunters() {
		r := s.Runners[i]
		s.Rings = append(s.Rings, Ring{X: r.X, Y: r.Y, Born: now})
	}
	for _, i := range s.Divers() {
		r := s.Runners[i]
		if !r.Alive {
			continue
		}
		mag := s.EffMag[i]
		if mag < 0.15 || s.HoldsSlot(i) {
			continue
		}
		if mag >= 0.6 {
			s.Blips = append(s.Blips, Blip{X: r.X, Y: r.Y, Str: 1.0, Until: now + 2.5})
		} else {
			s.Blips = append(s.Blips, Blip{X: r.X, Y: r.Y, Str: 0.5, Until: now + 1.0})
		}
	}
	s.EventID++
	s.LastPing = &PingEv{ID: s.EventID}
}

// Step advances the simulation by dt. now is the current unix time.
func (s *Sim) Step(dt, now float64) {
	if s.Phase == "countdown" && now >= s.CountdownEnd {
		s.Phase = "playing"
		s.RoundTimeEnd = now + RoundTime
		s.NextPingAt = now + 2.0
		return
	}
	if s.Phase == "round" && now >= s.RoundEnd {
		s.Round++
		s.AssignRoles(now)
		s.Phase = "playing"
		s.RoundTimeEnd = now + RoundTime
		s.NextPingAt = now + 2.0
		return
	}
	if s.Phase != "playing" {
		return
	}
	s.BotBrain(dt, now)
	if now >= s.NextPingAt {
		s.SonarPing(now)
	}
	for i := range MaxPlayers {
		role := s.Roles[i]
		if role == "" {
			continue
		}
		r := s.Runners[i]
		if role == "diver" && !r.Alive {
			continue
		}
		var ix, iy float64
		if p := s.Players[i]; p != nil {
			ix = math.Max(-1, math.Min(1, p.IX))
			iy = math.Max(-1, math.Min(1, p.IY))
		}
		mag := math.Hypot(ix, iy)
		if mag > 1 {
			ix /= mag
			iy /= mag
			mag = 1
		}
		var spd float64
		if role == "diver" {
			if mag > 0.6 {
				r.Stam = math.Max(0, r.Stam-0.25*dt)
			} else if mag < 0.35 {
				r.Stam = math.Min(1, r.Stam+0.18*dt)
				if r.Stam >= 0.3 {
					r.Gassed = false
				}
			}
			if r.Stam <= 0 {
				r.Gassed = true
			}
			if r.Gassed && mag > 0.3 {
				mag = 0.3
			}
			spd = DiverSpeed * mag
		} else {
			spd = HunterSpeed * mag
		}
		s.EffMag[i] = mag
		if s.HoldsSlot(i) && mag > 0.15 {
			s.ReleaseSlots(i)
		}
		r.X, r.Y = Collide(r.X+ix*spd*dt, r.Y+iy*spd*dt)
	}
	for _, i := range s.Divers() {
		r := s.Runners[i]
		if !r.Alive || s.HoldsSlot(i) {
			s.Channel[i] = nil
			continue
		}
		found := -1
		for ni, nd := range s.Nodes {
			if nd.Done >= 3 {
				continue
			}
			free := false
			for _, sl := range nd.Slots {
				if sl.Solver == -1 && !sl.Resolved {
					free = true
					break
				}
			}
			if !free {
				continue
			}
			if math.Hypot(r.X-nd.X, r.Y-nd.Y) < NodeR {
				found = ni
				break
			}
		}
		ch := s.Channel[i]
		if found == -1 {
			s.Channel[i] = nil
			continue
		}
		if ch == nil || ch.Node != found {
			s.Channel[i] = &Scan{Node: found, Since: now}
			continue
		}
		if now-ch.Since >= ChannelTime {
			nd := s.Nodes[found]
			for _, sl := range nd.Slots {
				if sl.Solver == -1 && !sl.Resolved {
					sl.Solver = i
					break
				}
			}
			s.Channel[i] = nil
			if p := s.Players[i]; p != nil && p.Bot {
				st := s.Bots[i]
				if st == nil {
					st = &BotState{}
					s.Bots[i] = st
				}
				st.SolveAt = now + BotSolveMin + s.rng.Float64()*(BotSolveMax-BotSolveMin)
				st.HasSol = true
			}
			by := s.playerName(i)
			s.Shout(nd.X, nd.Y, by, now)
		}
	}
	for _, h := range s.Hunters() {
		hr := s.Runners[h]
		for _, d := range s.Divers() {
			dr := s.Runners[d]
			if !dr.Alive {
				continue
			}
			if math.Hypot(hr.X-dr.X, hr.Y-dr.Y) < TagDist {
				dr.Alive = false
				s.ReleaseSlots(d)
				s.EventID++
				s.LastTag = &TagEv{Hunter: h + 1, Victim: d + 1, ID: s.EventID}
			}
		}
	}
	if divs := s.Divers(); len(divs) > 0 {
		anyAlive := false
		for _, d := range divs {
			if s.Runners[d].Alive {
				anyAlive = true
				break
			}
		}
		if !anyAlive {
			s.WinRound(1, "all divers tagged", now)
			return
		}
	}
	if s.Cracked >= s.Target {
		s.WinRound(2, "nodes cracked", now)
		return
	}
	if now >= s.RoundTimeEnd {
		s.WinRound(2, "survived the dark", now)
		return
	}
	kept := s.Rings[:0]
	for _, rg := range s.Rings {
		if now-rg.Born < 2.0 {
			kept = append(kept, rg)
		}
	}
	s.Rings = kept
	keptB := s.Blips[:0]
	for _, b := range s.Blips {
		if now < b.Until {
			keptB = append(keptB, b)
		}
	}
	s.Blips = keptB
}

func (s *Sim) playerName(seat int) string {
	if p := s.Players[seat]; p != nil && p.Name != "" {
		return p.Name
	}
	return "P" + strconv.Itoa(seat+1)
}

// SolveAttempt grades a wire-puzzle solution. links are [src,dst] pairs.
func (s *Sim) SolveAttempt(seat int, links [][2]int, ok bool, now float64) map[string]any {
	if !ok {
		return map[string]any{"ok": false}
	}
	for ni, nd := range s.Nodes {
		for _, sl := range nd.Slots {
			if sl.Solver == seat && !sl.Resolved {
				got := map[[2]int]bool{}
				for _, l := range links {
					got[l] = true
				}
				want := map[[2]int]bool{}
				for d, src := range sl.Perm {
					want[[2]int{src, d}] = true
				}
				match := len(got) == sl.N && len(got) == len(want)
				if match {
					for k := range got {
						if !want[k] {
							match = false
							break
						}
					}
				}
				if match {
					sl.Resolved = true
					sl.Solver = -1
					nd.Done++
					by := s.playerName(seat)
					s.Shout(nd.X, nd.Y, by, now)
					if nd.Done >= 3 {
						s.Cracked++
						s.EventID++
						s.LastCollect = &CollectEv{By: by, Done: s.Cracked,
							Target: s.Target, ID: s.EventID}
						s.Nodes = append(s.Nodes[:ni], s.Nodes[ni+1:]...)
						if s.Cracked < s.Target {
							s.SpawnNode()
						}
					}
					return map[string]any{"ok": true}
				}
				return map[string]any{"ok": false}
			}
		}
	}
	return map[string]any{"ok": false}
}

// --- bots ---

func (s *Sim) botPerceive(seat int, now float64) (hunters [][2]float64, nodes [][2]float64,
	blips [][2]float64, close [][2]float64, mates []int) {
	r := s.Runners[seat]
	if s.Roles[seat] == "diver" {
		for _, h := range s.Hunters() {
			hr := s.Runners[h]
			if math.Hypot(hr.X-r.X, hr.Y-r.Y) < 280 {
				hunters = append(hunters, [2]float64{hr.X, hr.Y})
			}
		}
		for _, nd := range s.Nodes {
			if nd.Done < 3 {
				nodes = append(nodes, [2]float64{nd.X, nd.Y})
			}
		}
		return
	}
	for _, d := range s.Divers() {
		dr := s.Runners[d]
		if dr.Alive && math.Hypot(dr.X-r.X, dr.Y-r.Y) < 90 {
			close = append(close, [2]float64{dr.X, dr.Y})
		}
	}
	for _, b := range s.Blips {
		if now < b.Until {
			blips = append(blips, [2]float64{b.X, b.Y})
		}
	}
	for _, h := range s.Hunters() {
		if h != seat {
			mates = append(mates, h)
		}
	}
	return
}

func nearest(x, y float64, pts [][2]float64) (float64, float64, float64) {
	best := math.MaxFloat64
	var bx, by float64
	for _, p := range pts {
		if d := math.Hypot(p[0]-x, p[1]-y); d < best {
			best, bx, by = d, p[0], p[1]
		}
	}
	return bx, by, best
}

func (s *Sim) botDiverMove(i int, now float64) (float64, float64) {
	r := s.Runners[i]
	if s.HoldsSlot(i) || s.Channel[i] != nil {
		return 0, 0
	}
	hunters, nodes, _, _, _ := s.botPerceive(i, now)
	if len(hunters) > 0 {
		hx, hy, _ := nearest(r.X, r.Y, hunters)
		dx, dy := r.X-hx, r.Y-hy
		n := math.Hypot(dx, dy)
		if n == 0 {
			n = 1
		}
		return dx / n, dy / n
	}
	if len(nodes) > 0 {
		nx, ny, n := nearest(r.X, r.Y, nodes)
		dx, dy := nx-r.X, ny-r.Y
		m := math.Hypot(dx, dy)
		if m == 0 {
			m = 1
		}
		_ = n
		if m < NodeR*0.5 {
			return 0, 0
		}
		return dx / m, dy / m
	}
	return 0, 0
}

func (s *Sim) botHunterMove(i int, st *BotState, now float64) (float64, float64) {
	r := s.Runners[i]
	_, _, blips, close, _ := s.botPerceive(i, now)
	targets := append(append([][2]float64{}, blips...), close...)
	if len(targets) > 0 {
		tx, ty, n := nearest(r.X, r.Y, targets)
		dx, dy := tx-r.X, ty-r.Y
		m := math.Hypot(dx, dy)
		if m == 0 {
			m = 1
		}
		_ = n
		if m < TagDist*0.5 {
			return 0, 0
		}
		return dx / m, dy / m
	}
	if !st.HasWp || math.Hypot(st.WpX-r.X, st.WpY-r.Y) < 40 || now-st.WpAt > 6 {
		x, y := 70+s.rng.Float64()*(W-140), 70+s.rng.Float64()*(H-140)
		for range 30 {
			x = 70 + s.rng.Float64()*(W-140)
			y = 70 + s.rng.Float64()*(H-140)
			if PointClear(x, y, 30) {
				break
			}
		}
		st.WpX, st.WpY, st.WpAt, st.HasWp = x, y, now, true
	}
	dx, dy := st.WpX-r.X, st.WpY-r.Y
	n := math.Hypot(dx, dy)
	if n == 0 {
		n = 1
	}
	return dx / n, dy / n
}

func (s *Sim) botTrySolve(i int, now float64) {
	for _, nd := range s.Nodes {
		for _, sl := range nd.Slots {
			if sl.Solver == i && !sl.Resolved {
				var links [][2]int
				for d, src := range sl.Perm {
					links = append(links, [2]int{src, d})
				}
				s.SolveAttempt(i, links, true, now)
				return
			}
		}
	}
}

func (s *Sim) BotBrain(dt, now float64) {
	_ = dt
	for i, p := range s.Players {
		if p == nil || !p.Bot {
			continue
		}
		p.LastSeen = now
		role := s.Roles[i]
		if s.Phase != "playing" || role == "" {
			p.IX, p.IY = 0, 0
			continue
		}
		if role == "diver" && !s.Runners[i].Alive {
			p.IX, p.IY = 0, 0
			continue
		}
		st := s.Bots[i]
		if st == nil {
			st = &BotState{}
			s.Bots[i] = st
		}
		var mx, my float64
		if role == "diver" {
			if s.HoldsSlot(i) && st.HasSol && now >= st.SolveAt {
				s.botTrySolve(i, now)
			}
			mx, my = s.botDiverMove(i, now)
		} else {
			mx, my = s.botHunterMove(i, st, now)
		}
		p.IX, p.IY = mx, my
	}
}

// SetBots adjusts the bot count. Returns current bot total.
func (s *Sim) SetBots(n int, now float64) int {
	var humans, bots []int
	for i, p := range s.Players {
		if p == nil {
			continue
		}
		if p.Bot {
			bots = append(bots, i)
		} else {
			humans = append(humans, i)
		}
	}
	if n < 0 {
		n = 0
	}
	if n > MaxPlayers-len(humans) {
		n = MaxPlayers - len(humans)
	}
	for len(bots) > n {
		i := bots[len(bots)-1]
		bots = bots[:len(bots)-1]
		s.ReleaseSlots(i)
		delete(s.HuntCounts, s.Players[i].ID)
		delete(s.Bots, i)
		s.Players[i] = nil
		s.Roles[i] = ""
		s.Channel[i] = nil
	}
	used := map[string]bool{}
	for _, p := range s.Players {
		if p != nil {
			used[p.Name] = true
		}
	}
	for len(bots) < n {
		seat := -1
		for i := range MaxPlayers {
			if s.Players[i] == nil {
				seat = i
				break
			}
		}
		name := ""
		for _, nm := range BotNames {
			if !used["🤖 "+nm] {
				name = nm
				break
			}
		}
		pid := "bot-" + name + "-" + strconv.Itoa(s.rng.Intn(1000000))
		s.Players[seat] = &Player{ID: pid, Name: "🤖 " + name, LastSeen: now, Ready: true, Bot: true}
		s.Bots[seat] = &BotState{}
		used["🤖 "+name] = true
		bots = append(bots, seat)
	}
	total := 0
	for _, p := range s.Players {
		if p != nil && p.Bot {
			total++
		}
	}
	return total
}

// ShouldStart mirrors the auto-start condition in game_loop.
func (s *Sim) ShouldStart() bool {
	var seated []*Player
	for _, p := range s.Players {
		if p != nil {
			seated = append(seated, p)
		}
	}
	if s.Phase != "waiting" || len(seated) < 2 {
		return false
	}
	for _, p := range seated {
		if !p.Ready {
			return false
		}
	}
	for _, p := range seated {
		if !p.Bot {
			return true
		}
	}
	return false
}

// EnterCountdown performs the countdown init from game_loop.
func (s *Sim) EnterCountdown(now float64) {
	s.Phase = "countdown"
	s.CountdownEnd = now + 2.4
	s.ResetMatch()
	s.AssignRoles(now)
}

// ShiftPaused thaws absolute deadlines (rings age out naturally, like Python).
func (s *Sim) ShiftPaused(d float64) {
	s.CountdownEnd += d
	s.RoundTimeEnd += d
	s.NextPingAt += d
	for k := range s.Blips {
		s.Blips[k].Until += d
	}
	for _, ch := range s.Channel {
		if ch != nil {
			ch.Since += d
		}
	}
}

func ParseLinks(v any) ([][2]int, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([][2]int, 0, len(arr))
	for _, e := range arr {
		pair, ok := e.([]any)
		if !ok || len(pair) != 2 {
			return nil, false
		}
		var nums [2]int
		for k, x := range pair {
			switch n := x.(type) {
			case float64:
				nums[k] = int(n)
			case string:
				t := strings.TrimSpace(n)
				iv, err := strconv.Atoi(t)
				if err != nil {
					return nil, false
				}
				nums[k] = iv
			default:
				return nil, false
			}
		}
		out = append(out, nums)
	}
	return out, true
}
