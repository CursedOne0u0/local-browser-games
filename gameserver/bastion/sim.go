// Package bastion is a Go port of the Bastion Bros LAN server (2players/bastion).
// Wire protocol and game logic are identical to server.py.
package bastion

import (
	"math"
	"math/rand"
	"time"
)

const VERSION = "1.1"

const (
	MaxSeats = 4
	MaxBase  = 20
	W        = 960.0
	H        = 600.0
	Cols     = 16
	Rows     = 10
	Cell     = 60.0
)

type TowerSpec struct {
	Cost         int
	Dmg          float64
	Rate         float64
	Range        float64
	Splash       float64
	Slow         float64
	SlowT        float64
	HP           float64
}

var Towers = map[string]TowerSpec{
	"arrow":  {Cost: 36, Dmg: 8, Rate: 1.5, Range: 150, HP: 100},
	"cannon": {Cost: 85, Dmg: 22, Rate: 0.7, Range: 170, Splash: 55, HP: 160},
	"frost":  {Cost: 65, Dmg: 3, Rate: 1.2, Range: 150, Slow: 0.55, SlowT: 2, HP: 120},
}

var TNames = []string{"arrow", "cannon", "frost"}

type cellT struct{ c, r int }

func buildPath() []cellT {
	var p []cellT
	for c := range 15 {
		p = append(p, cellT{c, 1})
	}
	p = append(p, cellT{14, 2}, cellT{14, 3}, cellT{14, 4})
	for c := 13; c >= 1; c-- {
		p = append(p, cellT{c, 4})
	}
	p = append(p, cellT{1, 5}, cellT{1, 6}, cellT{1, 7})
	for c := 2; c < 16; c++ {
		p = append(p, cellT{c, 7})
	}
	return p
}

var Path = buildPath()

var PathSet = func() map[cellT]bool {
	m := map[cellT]bool{}
	for _, c := range Path {
		m[c] = true
	}
	return m
}()

type Waypt struct{ X, Y float64 }

var Waypts = func() []Waypt {
	w := make([]Waypt, len(Path))
	for i, c := range Path {
		w[i] = Waypt{X: float64(c.c)*Cell + Cell/2, Y: float64(c.r)*Cell + Cell/2}
	}
	return w
}()

var SegLens = func() []float64 {
	s := make([]float64, len(Waypts)-1)
	for i := range s {
		s[i] = math.Hypot(Waypts[i+1].X-Waypts[i].X, Waypts[i+1].Y-Waypts[i].Y)
	}
	return s
}()

var EKind = map[string]struct{ HP, Spd float64; Bounty, Leak int }{
	"walker": {20, 55, 4, 1},
	"runner": {12, 95, 5, 1},
	"brute":  {70, 40, 10, 3},
	"lord":   {500, 44, 70, 6},
}

type Player struct {
	ID       string
	Name     string
	LastSeen float64
	Ready    bool
	IX, IY   float64
	Sel      int
	Build    bool
	X, Y     float64
}

type Tower struct {
	C, R int
	Type string
	HP   float64
	CD   float64
	X, Y float64
}

type Enemy struct {
	Kind         string
	X, Y         float64
	HP, MaxHP    float64
	Seg          int
	SegT, SegLen float64
	SlowT        float64
	SmashT       float64
	Spd          float64
	Dead         bool
}

type WaveEv struct {
	Level int `json:"level"`
	Wave  int `json:"wave"`
	ID    int `json:"id"`
}
type OverEv struct {
	Level int `json:"level"`
	ID    int `json:"id"`
}
type BuildEv struct {
	Seat int    `json:"seat"`
	What string `json:"what"`
	Type string `json:"type"`
	ID   int    `json:"id"`
}

type buildRes struct {
	Ok   bool
	Why  string
	What string
	Type string
}

type Sim struct {
	Phase        string
	CountdownEnd float64
	Level        int
	Wave         int
	Players      [MaxSeats]*Player
	Grid         []*Tower
	Gold         int
	Base         int
	Enemies      []*Enemy
	SpawnQueue   []string
	SpawnT       float64
	Winner       int
	EventID      int
	BuildEnd     float64
	LastWave     *WaveEv
	LastOver     *OverEv
	LastBuild    *BuildEv
	Paused       bool
	PausedBy     string
	PausedSince  float64
	rng          *rand.Rand
}

func New() *Sim {
	return &Sim{Phase: "waiting", Base: MaxBase,
		rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

func unixNow() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func EnemyHP(kind string, level int) float64 {
	m := 1 + 0.28*float64(level-1)
	return math.Round(EKind[kind].HP * m)
}

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
		if p != nil && now-p.LastSeen > 8 {
			s.Players[i] = nil
		}
	}
}

func (s *Sim) Occupied() []int {
	var out []int
	for i, p := range s.Players {
		if p != nil {
			out = append(out, i)
		}
	}
	return out
}

// --- build ---

func (s *Sim) TileFree(c, r int) bool {
	if c < 0 || c >= Cols || r < 0 || r >= Rows {
		return false
	}
	if PathSet[cellT{c, r}] {
		return false
	}
	for _, t := range s.Grid {
		if t.C == c && t.R == r {
			return false
		}
	}
	return true
}

func (s *Sim) TowerCost(ttype string) int {
	base := Towers[ttype].Cost
	if s.Phase == "combat" {
		return int(math.Ceil(float64(base) * 1.25))
	}
	return base
}

func (s *Sim) TryBuild(i int) buildRes {
	p := s.Players[i]
	c, r := int(p.X/Cell), int(p.Y/Cell)
	ttype := TNames[p.Sel%3]
	for _, t := range s.Grid {
		if t.C == c && t.R == r {
			mx := Towers[t.Type].HP
			if t.HP >= mx {
				return buildRes{Why: "full"}
			}
			cost := int(math.Ceil(float64(Towers[t.Type].Cost) * 0.75))
			if s.Gold < cost {
				return buildRes{Why: "poor"}
			}
			s.Gold -= cost
			t.HP = mx
			return buildRes{Ok: true, What: "repair"}
		}
	}
	if !s.TileFree(c, r) {
		return buildRes{Why: "blocked"}
	}
	cost := s.TowerCost(ttype)
	if s.Gold < cost {
		return buildRes{Why: "poor"}
	}
	s.Gold -= cost
	s.Grid = append(s.Grid, &Tower{C: c, R: r, Type: ttype, HP: Towers[ttype].HP,
		X: float64(c)*Cell + Cell/2, Y: float64(r)*Cell + Cell/2})
	return buildRes{Ok: true, What: "build", Type: ttype}
}

func (s *Sim) NextLevel(now float64) {
	s.Level++
	s.Wave = 0
	s.Phase = "build"
	s.BuildEnd = now + 10
	s.EventID++
	s.LastWave = &WaveEv{Level: s.Level, Wave: 0, ID: s.EventID}
}

func (s *Sim) BuildWave() []string {
	lv, wv := s.Level, s.Wave+1
	n := 7 + wv*3 + lv*2
	var unlock []string
	switch {
	case lv >= 3:
		unlock = []string{"walker", "walker", "runner", "runner", "brute"}
	case lv >= 2:
		unlock = []string{"walker", "walker", "walker", "runner", "brute"}
	default:
		unlock = []string{"walker", "walker", "walker", "runner"}
	}
	q := make([]string, 0, n+1)
	for range n {
		q = append(q, unlock[s.rng.Intn(len(unlock))])
	}
	if wv%3 == 0 {
		q = append(q, "lord")
	}
	s.rng.Shuffle(len(q), func(a, b int) { q[a], q[b] = q[b], q[a] })
	return q
}

func (s *Sim) StartWave() bool {
	if s.Phase != "build" {
		return false
	}
	s.Wave++
	s.SpawnQueue = s.BuildWave()
	s.SpawnT = 0
	s.Phase = "combat"
	s.EventID++
	s.LastWave = &WaveEv{Level: s.Level, Wave: s.Wave, ID: s.EventID}
	return true
}

func (s *Sim) SpawnInterval() float64 {
	return math.Max(0.35, 2.0-0.18*float64(s.Wave+s.Level))
}

func (s *Sim) SpawnEnemy(kind string) {
	lv := s.Level
	spd := map[string]float64{"walker": 55, "runner": 95, "brute": 40, "lord": 44}[kind] *
		(1 + 0.04*float64(lv-1))
	hp := EnemyHP(kind, lv)
	s.Enemies = append(s.Enemies, &Enemy{Kind: kind, SegLen: SegLens[0],
		Spd: spd, HP: hp, MaxHP: hp, X: Waypts[0].X, Y: Waypts[0].Y})
}

func (s *Sim) HurtFoe(e *Enemy, dmg float64) {
	e.HP -= dmg
	if e.HP <= 0 && !e.Dead {
		e.Dead = true
		s.Gold += EKind[e.Kind].Bounty
	}
}

func (s *Sim) GameOver() {
	s.Phase = "over"
	s.EventID++
	s.LastOver = &OverEv{Level: s.Level, ID: s.EventID}
}

// ShouldStart mirrors the auto-start condition in game_loop.
func (s *Sim) ShouldStart() bool {
	occ := s.Occupied()
	if s.Phase != "waiting" || len(occ) < 2 {
		return false
	}
	for _, i := range occ {
		if !s.Players[i].Ready {
			return false
		}
	}
	return true
}

// EnterCountdown performs the countdown init from game_loop.
func (s *Sim) EnterCountdown(now float64) {
	s.Phase = "countdown"
	s.CountdownEnd = now + 2.4
	s.Level, s.Wave = 0, 0
	s.Grid = nil
	s.Gold = 90
	s.Base = MaxBase
	s.Enemies = nil
	s.SpawnQueue = nil
	s.Winner = 0
	s.NextLevel(now)
	s.LastOver = nil
}

// ShiftPaused thaws absolute deadlines.
func (s *Sim) ShiftPaused(d float64) {
	s.CountdownEnd += d
	s.BuildEnd += d
}

// Step advances the simulation by dt. now is the current unix time.
func (s *Sim) Step(dt, now float64) {
	if s.Phase == "countdown" && now >= s.CountdownEnd {
		s.Phase = "build"
		return
	}
	for _, i := range s.Occupied() {
		p := s.Players[i]
		ix := math.Max(-1, math.Min(1, p.IX))
		iy := math.Max(-1, math.Min(1, p.IY))
		n := math.Hypot(ix, iy)
		if n > 0.05 {
			p.X = math.Max(20, math.Min(W-20, p.X+ix*340*dt))
			p.Y = math.Max(20, math.Min(H-20, p.Y+iy*340*dt))
		}
		if p.Build {
			p.Build = false
			if r := s.TryBuild(i); r.Ok {
				s.EventID++
				s.LastBuild = &BuildEv{Seat: i + 1, What: r.What, Type: r.Type, ID: s.EventID}
			}
		}
	}
	if s.Phase == "build" && now >= s.BuildEnd {
		s.StartWave()
		return
	}
	if s.Phase != "combat" {
		return
	}
	if len(s.SpawnQueue) > 0 {
		s.SpawnT -= dt
		if s.SpawnT <= 0 {
			s.SpawnT = s.SpawnInterval()
			kind := s.SpawnQueue[len(s.SpawnQueue)-1]
			s.SpawnQueue = s.SpawnQueue[:len(s.SpawnQueue)-1]
			s.SpawnEnemy(kind)
		}
	}
	for _, t := range s.Grid {
		t.CD -= dt
		T := Towers[t.Type]
		var best *Enemy
		bd := T.Range * T.Range
		for _, e := range s.Enemies {
			if e.Dead {
				continue
			}
			if d := (e.X-t.X)*(e.X-t.X) + (e.Y-t.Y)*(e.Y-t.Y); d < bd {
				bd, best = d, e
			}
		}
		if best != nil && t.CD <= 0 {
			t.CD = 1 / T.Rate
			switch t.Type {
			case "cannon":
				for _, e := range s.Enemies {
					if !e.Dead && math.Hypot(e.X-best.X, e.Y-best.Y) < T.Splash {
						s.HurtFoe(e, T.Dmg)
					}
				}
			case "frost":
				s.HurtFoe(best, T.Dmg)
				best.SlowT = T.SlowT
			default:
				s.HurtFoe(best, T.Dmg)
			}
		}
	}
	for _, e := range s.Enemies {
		if e.Dead {
			continue
		}
		if e.SlowT > 0 {
			e.SlowT -= dt
		}
		mult := 1.0
		if e.SlowT > 0 {
			mult = 0.55
		}
		e.SegT += e.Spd * mult * dt
		for e.SegT >= e.SegLen {
			e.SegT -= e.SegLen
			e.Seg++
			if e.Seg >= len(SegLens) {
				s.Base -= EKind[e.Kind].Leak
				e.Dead = true
				if s.Base <= 0 {
					s.Base = 0
					s.GameOver()
					return
				}
				break
			}
			e.SegLen = SegLens[e.Seg]
		}
		if e.Dead {
			continue
		}
		a, b := Waypts[e.Seg], Waypts[e.Seg+1]
		f := 0.0
		if e.SegLen != 0 {
			f = e.SegT / e.SegLen
		}
		e.X, e.Y = a.X+(b.X-a.X)*f, a.Y+(b.Y-a.Y)*f
		if e.Kind == "brute" || e.Kind == "lord" {
			e.SmashT -= dt
			if e.SmashT <= 0 {
				e.SmashT = 1
				for _, t := range s.Grid {
					if math.Hypot(t.X-e.X, t.Y-e.Y) < 50 {
						t.HP -= 10
					}
				}
			}
		}
	}
	keptG := s.Grid[:0]
	for _, t := range s.Grid {
		if t.HP > 0 {
			keptG = append(keptG, t)
		}
	}
	s.Grid = keptG
	keptE := s.Enemies[:0]
	for _, e := range s.Enemies {
		if !e.Dead {
			keptE = append(keptE, e)
		}
	}
	s.Enemies = keptE
	if len(s.SpawnQueue) == 0 && len(s.Enemies) == 0 && s.Phase == "combat" {
		s.Gold += 15 + 6*s.Level
		for _, t := range s.Grid {
			t.HP = math.Min(Towers[t.Type].HP, t.HP+Towers[t.Type].HP*0.10)
		}
		if s.Wave >= s.Level+2 {
			s.NextLevel(now)
		} else {
			s.Phase = "build"
			s.BuildEnd = now + 10
			s.EventID++
			s.LastWave = &WaveEv{Level: s.Level, Wave: 0, ID: s.EventID}
		}
	}
}

func r1(v float64) float64 { return math.Round(v*10) / 10 }
