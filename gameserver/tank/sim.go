// Package tank is a Go port of the Tank Duel LAN server (2players/tank-duel).
// Wire protocol and game logic are identical to server.py; only the maze
// layout differs per round (as in Python, it is randomly generated).
package tank

import (
	"math"
	"math/rand"
	"time"
)

const VERSION = "1.25"

const (
	WinRounds = 5
	Cols      = 17
	Rows      = 12
	Cell      = 60
	TankR     = 16.0
	TankSpeed = 175.0
	TurnSpeed = 3.4
	BulletSpd = 430.0
	FireCD    = 0.4
	RapidCD   = 0.12
	MaxBullets   = 5
	BulletBounces = 6
	BulletLife    = 5.0
	OwnerGrace    = 0.4
	HomeFov       = 125.0 * math.Pi / 180.0
	MaxPickups    = 2
	MaxMines      = 3
	MineArm       = 1.0
	MineRadius    = 44.0
	RailCharge    = 0.9
	RailRange     = 950.0
	RailHalfW     = 24.0
)

var classicMaze = [Rows]string{
	"#################",
	"#.......#.......#",
	"#.#####.#.#####.#",
	"#.#...#...#...#.#",
	"#.#.#.#####.#.#.#",
	"#...#.......#...#",
	"#...#.......#...#",
	"#.#.#.#####.#.#.#",
	"#.#...#...#...#.#",
	"#.#####.#.#####.#",
	"#.......#.......#",
	"#################",
}

var weaponDur = map[string]float64{
	"spread": 10.0, "rapid": 8.0, "shield": 12.0,
	"homing": 9.0, "mines": 14.0, "rail": 8.0,
}
var weaponKinds = []string{"spread", "rapid", "shield", "homing", "mines", "rail"}

type spawnT struct{ cx, cy, ang float64 }

var spawns = [2]spawnT{
	{1.5, 10.5, -math.Pi / 2},
	{15.5, 1.5, math.Pi / 2},
}

// spawnCells are 180° rotational mirrors of each other.
var spawnCells = [2][2]int{{1, Rows - 2}, {Cols - 2, 1}}

type Player struct {
	ID       string
	Name     string
	LastSeen float64
	Ready    bool
	Fwd      float64
	Turn     float64
	Fire     bool
	PrevFire bool
}

type Tank struct {
	X, Y       float64
	Ang        float64
	Score      int
	Alive      bool
	CD         float64
	Wpn        string
	WpnUntil   float64
	RailCharge float64
	RailAng    float64
}

type Bullet struct {
	X, Y, VX, VY float64
	Owner        int
	Bounces      int
	Born, Grace float64
	Home        bool
	Dead        bool
}

type Mine struct {
	X, Y    float64
	Owner   int
	ArmedAt float64
	Dead    bool
}

type Pickup struct {
	X, Y  float64
	Kind  string
	Born  float64
	Dead  bool
}

type KillEv struct {
	By      int  `json:"by"`
	Victim  int  `json:"victim"`
	Suicide bool `json:"suicide"`
	ID      int  `json:"id"`
}
type BlockEv struct {
	Tank int `json:"tank"`
	ID   int `json:"id"`
}
type PickupEv struct {
	Tank int    `json:"tank"`
	Kind string `json:"kind"`
	ID   int    `json:"id"`
}
type ShotEv struct {
	By   int    `json:"by"`
	Kind string `json:"kind,omitempty"`
	ID   int    `json:"id"`
}
type BeamEv struct {
	X1 float64 `json:"x1"`
	Y1 float64 `json:"y1"`
	X2 float64 `json:"x2"`
	Y2 float64 `json:"y2"`
	By int     `json:"by"`
	ID int     `json:"id"`
}
type PopEv struct {
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
	ID int     `json:"id"`
}

type Sim struct {
	Phase        string
	CountdownEnd float64
	RoundEnd     float64
	RoundScored  int
	Round        int
	Players      [2]*Player
	Tanks        [2]*Tank
	Bullets      []*Bullet
	Mines        []*Mine
	Pickups      []*Pickup
	PickupTimer  float64
	Winner       int
	EventID      int
	LastKill     *KillEv
	LastBlock    *BlockEv
	LastPickup   *PickupEv
	LastShot     *ShotEv
	LastBeam     *BeamEv
	LastPop      *PopEv
	BounceN      int
	Paused       bool
	PausedBy     string
	PausedSince  float64
	Maze         [Rows]string
	rng          *rand.Rand
}

func New() *Sim {
	s := &Sim{Phase: "waiting", Round: 1, rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
	s.Tanks[0] = &Tank{}
	s.Tanks[1] = &Tank{}
	s.ResetTanks(unixNow())
	return s
}

func unixNow() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

// --- maze ---

func (s *Sim) genMaze() [Rows]string {
	mirror := func(c, r int) (int, int) { return Cols - 1 - c, Rows - 1 - r }
	for range 200 {
		var g [Rows][Cols]byte
		for r := range Rows {
			for c := range Cols {
				g[r][c] = '#'
			}
		}
		for r := 1; r < Rows-1; r++ {
			for c := 1; c < Cols-1; c++ {
				mc, mr := mirror(c, r)
				if c > mc || (c == mc && r > mr) {
					continue
				}
				ch := byte('.')
				if s.rng.Float64() < 0.30 {
					ch = '#'
				}
				g[r][c], g[mr][mc] = ch, ch
			}
		}
		for _, cell := range spawnCells {
			for dc := -1; dc <= 1; dc++ {
				for dr := -1; dr <= 1; dr++ {
					c2, r2 := cell[0]+dc, cell[1]+dr
					if 1 <= c2 && c2 < Cols-1 && 1 <= r2 && r2 < Rows-1 {
						g[r2][c2] = '.'
					}
				}
			}
		}
		open := 0
		for r := range Rows {
			for c := range Cols {
				if g[r][c] == '.' {
					open++
				}
			}
		}
		if float64(open) < float64(Cols-2)*float64(Rows-2)*0.55 {
			continue
		}
		s1, s2 := spawnCells[0], spawnCells[1]
		seen := map[[2]int]bool{s1: true}
		stack := [][2]int{s1}
		for len(stack) > 0 {
			c, r := stack[len(stack)-1][0], stack[len(stack)-1][1]
			stack = stack[:len(stack)-1]
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				n := [2]int{c + d[0], r + d[1]}
				if 0 <= n[0] && n[0] < Cols && 0 <= n[1] && n[1] < Rows &&
					g[n[1]][n[0]] == '.' && !seen[n] {
					seen[n] = true
					stack = append(stack, n)
				}
			}
		}
		if seen[s2] {
			var out [Rows]string
			for r := range Rows {
				out[r] = string(g[r][:])
			}
			return out
		}
	}
	return classicMaze
}

func (s *Sim) isWall(px, py float64) bool {
	c, r := int(px/Cell), int(py/Cell)
	if c < 0 || r < 0 || c >= Cols || r >= Rows {
		return true
	}
	return s.Maze[r][c] == '#'
}

func (s *Sim) circleFree(x, y float64) bool {
	rad := TankR
	for _, o := range [][2]float64{{-rad, 0}, {rad, 0}, {0, -rad}, {0, rad},
		{-rad * 0.7, -rad * 0.7}, {rad * 0.7, rad * 0.7},
		{-rad * 0.7, rad * 0.7}, {rad * 0.7, -rad * 0.7}} {
		if s.isWall(x+o[0], y+o[1]) {
			return false
		}
	}
	return true
}

// --- setup ---

func (s *Sim) ResetTanks(now float64) {
	_ = now
	s.Maze = s.genMaze()
	for i, sp := range spawns {
		t := s.Tanks[i]
		t.X, t.Y = sp.cx*Cell, sp.cy*Cell
		t.Ang = sp.ang
		t.Alive = true
		t.CD = 0
		t.Wpn = ""
		t.WpnUntil = 0
		t.RailCharge = 0
		t.RailAng = 0
	}
	s.Bullets = nil
	s.Mines = nil
	s.Pickups = nil
	s.PickupTimer = 5.0
}

func (s *Sim) openCell() (float64, float64, bool) {
	for range 40 {
		c := 1 + s.rng.Intn(Cols-2)
		r := 1 + s.rng.Intn(Rows-2)
		if s.Maze[r][c] != '.' {
			continue
		}
		x, y := (float64(c)+0.5)*Cell, (float64(r)+0.5)*Cell
		ok := true
		for _, t := range s.Tanks {
			if math.Hypot(x-t.X, y-t.Y) <= 3*Cell {
				ok = false
				break
			}
		}
		if ok {
			return x, y, true
		}
	}
	return 0, 0, false
}

func (s *Sim) spawnPickup(now float64) {
	if len(s.Pickups) >= MaxPickups {
		return
	}
	x, y, ok := s.openCell()
	if !ok {
		return
	}
	s.Pickups = append(s.Pickups, &Pickup{X: x, Y: y,
		Kind: weaponKinds[s.rng.Intn(len(weaponKinds))], Born: now})
}

func (s *Sim) ResetScores() {
	for _, t := range s.Tanks {
		t.Score = 0
	}
	s.Winner = 0
	s.Round = 1
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

// --- combat ---

func (s *Sim) fireBullet(i int, now float64) {
	t := s.Tanks[i]
	if !t.Alive || t.CD > 0 {
		return
	}
	var wpn string
	if now < t.WpnUntil {
		wpn = t.Wpn
	}
	if wpn == "mines" {
		n := 0
		for _, m := range s.Mines {
			if m.Owner == i {
				n++
			}
		}
		if n >= MaxMines {
			return
		}
		t.CD = 0.8
		s.EventID++
		s.LastShot = &ShotEv{By: i + 1, ID: s.EventID}
		s.Mines = append(s.Mines, &Mine{
			X: t.X - math.Cos(t.Ang)*(TankR+6),
			Y: t.Y - math.Sin(t.Ang)*(TankR+6),
			Owner: i, ArmedAt: now + MineArm})
		return
	}
	active := 0
	for _, b := range s.Bullets {
		if b.Owner == i {
			active++
		}
	}
	var shots []float64
	if wpn == "spread" {
		shots = []float64{-0.26, 0.0, 0.26}
	} else {
		shots = []float64{0.0}
	}
	allow := MaxBullets - active
	if allow < 1 {
		allow = 1
	}
	if allow < len(shots) {
		shots = shots[:allow]
	}
	if len(shots) == 0 {
		return
	}
	if wpn == "rapid" {
		t.CD = RapidCD
	} else {
		t.CD = FireCD
	}
	s.EventID++
	if wpn == "rail" {
		t.RailCharge = RailCharge
		t.RailAng = t.Ang
		t.CD = RailCharge + 0.5
		s.LastShot = &ShotEv{By: i + 1, Kind: "charge", ID: s.EventID}
		return
	}
	s.LastShot = &ShotEv{By: i + 1, Kind: "shell", ID: s.EventID}
	for _, off := range shots {
		a := t.Ang + off
		s.Bullets = append(s.Bullets, &Bullet{
			X: t.X + math.Cos(a)*(TankR+8), Y: t.Y + math.Sin(a)*(TankR+8),
			VX: math.Cos(a) * BulletSpd, VY: math.Sin(a) * BulletSpd,
			Owner: i, Born: now, Grace: now + OwnerGrace, Home: wpn == "homing",
		})
	}
}

func (s *Sim) fireRail(i int, now float64) {
	t := s.Tanks[i]
	ang := t.RailAng
	x1, y1 := t.X, t.Y
	dx, dy := math.Cos(ang), math.Sin(ang)
	d := 8.0
	for d < RailRange && !s.isWall(x1+dx*d, y1+dy*d) {
		d += 8.0
	}
	best, bestt := -1, 0.0
	hx, hy := x1+dx*d, y1+dy*d
	for j := range 2 {
		if j == i {
			continue
		}
		o := s.Tanks[j]
		if !o.Alive {
			continue
		}
		tt := (o.X-x1)*dx + (o.Y-y1)*dy
		if tt < 0 || tt > d {
			continue
		}
		dd := math.Hypot(o.X-(x1+dx*tt), o.Y-(y1+dy*tt))
		if dd < TankR+RailHalfW && (best == -1 || tt < bestt) {
			best, bestt = j, tt
			hx, hy = x1+dx*tt, y1+dy*tt
		}
	}
	s.EventID++
	s.LastBeam = &BeamEv{X1: r1(x1), Y1: r1(y1), X2: r1(hx), Y2: r1(hy), By: i + 1, ID: s.EventID}
	if best != -1 {
		o := s.Tanks[best]
		if o.Wpn == "shield" && now < o.WpnUntil {
			o.Wpn = ""
			o.WpnUntil = 0
			s.EventID++
			s.LastBlock = &BlockEv{Tank: best + 1, ID: s.EventID}
		} else {
			s.kill(best, i, now)
		}
	}
}

func (s *Sim) kill(victim, by int, now float64) {
	s.Tanks[victim].Alive = false
	scorer := by
	if victim == by {
		scorer = 1 - victim
	}
	s.Tanks[scorer].Score++
	s.Bullets = nil
	s.EventID++
	s.LastKill = &KillEv{By: scorer + 1, Victim: victim + 1, Suicide: victim == by, ID: s.EventID}
	if s.Tanks[scorer].Score >= WinRounds {
		s.Phase = "over"
		s.Winner = scorer + 1
	} else {
		s.Phase = "round"
		s.RoundScored = scorer + 1
		s.RoundEnd = now + 2.0
	}
}

// Step advances the simulation by dt. now is the current unix time.
func (s *Sim) Step(dt, now float64) {
	if s.Phase == "countdown" && now >= s.CountdownEnd {
		s.Phase = "playing"
		return
	}
	if s.Phase == "round" && now >= s.RoundEnd {
		s.Round++
		s.ResetTanks(now)
		s.Phase = "playing"
		return
	}
	if s.Phase != "playing" {
		return
	}
	for i := range 2 {
		t := s.Tanks[i]
		if !t.Alive {
			continue
		}
		t.CD = math.Max(0, t.CD-dt)
		if t.RailCharge > 0 {
			t.RailCharge -= dt
			if t.RailCharge <= 0 {
				t.RailCharge = 0
				if t.Alive {
					s.fireRail(i, now)
				}
			}
		}
		p := s.Players[i]
		fwd, turn := 0.0, 0.0
		if p != nil {
			fwd = clamp1(p.Fwd)
			turn = clamp1(p.Turn)
			rapid := t.Wpn == "rapid" && now < t.WpnUntil
			if p.Fire && (!p.PrevFire || rapid) {
				s.fireBullet(i, now)
			}
			p.PrevFire = p.Fire
		}
		t.Ang += turn * TurnSpeed * dt
		if nx := t.X + math.Cos(t.Ang)*fwd*TankSpeed*dt; s.circleFree(nx, t.Y) {
			t.X = nx
		}
		if ny := t.Y + math.Sin(t.Ang)*fwd*TankSpeed*dt; s.circleFree(t.X, ny) {
			t.Y = ny
		}
	}
	for _, b := range s.Bullets {
		if b.Home {
			foe := s.Tanks[1-b.Owner]
			ot := s.Tanks[b.Owner]
			if foe.Alive && ot.Wpn == "homing" && now < ot.WpnUntil {
				sp := math.Hypot(b.VX, b.VY)
				if sp == 0 {
					sp = 1
				}
				cur := math.Atan2(b.VY, b.VX)
				want := math.Atan2(foe.Y-b.Y, foe.X-b.X)
				dd := math.Mod(want-cur+math.Pi, 2*math.Pi) - math.Pi
				if math.Abs(dd) <= HomeFov/2 {
					na := cur + clamp(2.8*dt, dd)
					b.VX = math.Cos(na) * sp
					b.VY = math.Sin(na) * sp
				}
			}
		}
		sdt := dt / 3
		for range 3 {
			oldx, oldy := b.X, b.Y
			b.X += b.VX * sdt
			b.Y += b.VY * sdt
			if s.isWall(b.X, b.Y) {
				if !s.isWall(oldx, b.Y) {
					b.X = oldx
					b.VX *= -1
				} else if !s.isWall(b.X, oldy) {
					b.Y = oldy
					b.VY *= -1
				} else {
					b.X, b.Y = oldx, oldy
					b.VX *= -1
					b.VY *= -1
				}
				b.Bounces++
				s.BounceN++
				if b.Bounces > BulletBounces {
					b.Dead = true
					s.EventID++
					s.LastPop = &PopEv{X: r1(b.X), Y: r1(b.Y), ID: s.EventID}
				}
				break
			}
		}
		for i := range 2 {
			t := s.Tanks[i]
			if !t.Alive {
				continue
			}
			if i == b.Owner && now < b.Grace {
				continue
			}
			if math.Hypot(b.X-t.X, b.Y-t.Y) < TankR+5 {
				b.Dead = true
				if t.Wpn == "shield" && now < t.WpnUntil {
					t.Wpn = ""
					t.WpnUntil = 0
					s.EventID++
					s.LastBlock = &BlockEv{Tank: i + 1, ID: s.EventID}
				} else {
					s.kill(i, b.Owner, now)
				}
				break
			}
		}
	}
	kept := s.Bullets[:0]
	for _, b := range s.Bullets {
		if !b.Dead && b.Bounces <= BulletBounces && now-b.Born < BulletLife {
			kept = append(kept, b)
		}
	}
	s.Bullets = kept
	for _, m := range s.Mines {
		if now < m.ArmedAt {
			continue
		}
		for _, i := range [2]int{1 - m.Owner, m.Owner} {
			t := s.Tanks[i]
			if t.Alive && math.Hypot(m.X-t.X, m.Y-t.Y) < MineRadius {
				m.Dead = true
				s.kill(i, m.Owner, now)
				break
			}
		}
		if s.Phase != "playing" {
			break
		}
	}
	keptM := s.Mines[:0]
	for _, m := range s.Mines {
		if !m.Dead {
			keptM = append(keptM, m)
		}
	}
	s.Mines = keptM
	s.PickupTimer -= dt
	if s.PickupTimer <= 0 {
		s.spawnPickup(now)
		s.PickupTimer = 6 + s.rng.Float64()*4
	}
	for _, pk := range s.Pickups {
		for i := range 2 {
			t := s.Tanks[i]
			if t.Alive && math.Hypot(pk.X-t.X, pk.Y-t.Y) < 24 {
				t.Wpn = pk.Kind
				t.WpnUntil = now + weaponDur[pk.Kind]
				pk.Dead = true
				s.EventID++
				s.LastPickup = &PickupEv{Tank: i + 1, Kind: pk.Kind, ID: s.EventID}
				break
			}
		}
	}
	keptP := s.Pickups[:0]
	for _, pk := range s.Pickups {
		if !pk.Dead && now-pk.Born < 15 {
			keptP = append(keptP, pk)
		}
	}
	s.Pickups = keptP
	for _, t := range s.Tanks {
		if t.Wpn != "" && now >= t.WpnUntil {
			t.Wpn = ""
		}
	}
}

// ShiftPaused thaws every absolute deadline by d (presence timers untouched).
func (s *Sim) ShiftPaused(d float64) {
	s.CountdownEnd += d
	s.RoundEnd += d
	for _, t := range s.Tanks {
		if t.WpnUntil != 0 {
			t.WpnUntil += d
		}
	}
	for _, m := range s.Mines {
		m.ArmedAt += d
	}
	for _, b := range s.Bullets {
		b.Born += d
		b.Grace += d
	}
	for _, p := range s.Pickups {
		p.Born += d
	}
}

func clamp1(v float64) float64 {
	return math.Max(-1, math.Min(1, v))
}

func clamp(lim, v float64) float64 {
	return math.Max(-lim, math.Min(lim, v))
}

func r1(v float64) float64 { return math.Round(v*10) / 10 }
func r3(v float64) float64 { return math.Round(v*1000) / 1000 }
