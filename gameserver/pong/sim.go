// Package pong is a Go port of the Neon Pong Showdown LAN server (hybrid1-2/neon-pong).
// Wire protocol and game logic are identical to server.py.
package pong

import (
	"math"
	"math/rand"
	"time"
)

const VERSION = "1.38"

const (
	W            = 800.0
	H            = 500.0
	BasePaddleH  = 90.0
	WindAccel    = 120.0
	TauntCD      = 2.5
	WinDefault   = 7
	SpeedDefault = 420.0
)

var Taunts = map[string]string{
	"gg": "GG! 🏓", "nice": "Nice shot! 🔥", "ouch": "Ouch! 😅",
	"whoops": "Whoops! 🙈", "lol": "LOL 😂", "rematch": "Rematch? 👀",
}

var powerupKinds = []string{"expand", "shrink", "turbo", "slow", "shield",
	"freeze", "magnet", "ghost", "swap", "vortex"}

type Player struct {
	ID           string
	Name         string
	Y            float64
	VY           float64
	PrevY        float64
	PrevT        float64
	Ready        bool
	LastSeen     float64
	Shield       bool
	FrozenUntil  float64
	OdUntil      float64
	OdReadyAt    float64
	MagnetUntil  float64
	TauntReadyAt float64
}

type Paddle struct {
	Y           float64
	H           float64
	Score       int
	EffectUntil float64
}

type Ball struct {
	X, Y  float64
	VX, VY float64
	Speed float64
	LastHit int
	Spin  float64
}

type Powerup struct {
	X, Y  float64
	Kind  string
	Born  float64
}

type Obstacle struct {
	X, Y  float64
	Ang   float64
	Len   float64
	Phase string
	Until float64
}

type Vortex struct {
	X, Y  float64
	Until float64
	ID    int
}

type PowerEv struct {
	Kind string `json:"kind"`
	By   int    `json:"by"`
	ID   int    `json:"id"`
}

type PointEv struct {
	Scored int   `json:"scored"`
	Scores [2]int `json:"scores"`
	ID     int   `json:"id"`
}

type BlockEv struct {
	By int `json:"by"`
	ID int `json:"id"`
}

type BounceEv struct {
	ID int `json:"id"`
}

type TauntEv struct {
	By   int    `json:"by"`
	Key  string `json:"key"`
	Text string `json:"text"`
	ID   int    `json:"id"`
}

type Settings struct {
	Win      int  `json:"win"`
	Speed    int  `json:"speed"`
	Obstacle bool `json:"obstacle"`
}

type Sim struct {
	Phase             string
	CountdownEnd      float64
	PointEnd          float64
	PointScored       int
	Players           [2]*Player
	P1, P2            *Paddle
	Ball              *Ball
	Rally             int
	Wind              int
	Powerup           *Powerup
	PowerupTimer      float64
	ServeDir          int
	Winner            int
	Sudden            bool
	ServePreviewUntil float64
	ServeVX, ServeVY   float64
	ServeCurrentDir   int
	ServeArmed        bool
	Obstacle          *Obstacle
	ObNext            float64
	LastBounce        *BounceEv
	GhostUntil        float64
	GhostHiddenFor    int
	Settings          Settings
	LastTaunt         *TauntEv
	Vortex            *Vortex
	Bot               bool
	BotVY             float64
	BotPrevY          float64
	BotPrevT          float64
	BotErr            float64
	Paused            bool
	PausedBy          string
	PausedSince       float64
	EventID           int
	LastPower         *PowerEv
	LastPoint         *PointEv
	LastBlock         *BlockEv
	rng               *rand.Rand
}

func New() *Sim {
	s := &Sim{rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
	s.P1 = &Paddle{Y: 0.5, H: BasePaddleH}
	s.P2 = &Paddle{Y: 0.5, H: BasePaddleH}
	s.Ball = &Ball{X: W / 2, Y: H / 2, Speed: SpeedDefault}
	s.Settings = Settings{Win: WinDefault, Speed: int(SpeedDefault), Obstacle: true}
	s.ServeCurrentDir = 1
	s.Phase = "waiting"
	s.ServeDir = 1
	s.PowerupTimer = 5
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
		if p != nil && now-p.LastSeen > 8 {
			s.Players[i] = nil
		}
	}
}

// --- setup ---

func (s *Sim) ResetPositions() {
	s.P1.Y, s.P2.Y = 0.5, 0.5
	s.P1.H, s.P2.H = BasePaddleH, BasePaddleH
	s.Sudden = false
}

func (s *Sim) ResetScores() {
	s.P1.Score, s.P2.Score = 0, 0
	s.Winner = 0
	s.Rally = 0
	s.Sudden = false
	s.LastBlock = nil
}

func (s *Sim) Serve(now float64) {
	b := s.Ball
	b.X = W / 2
	b.Y = 120 + s.rng.Float64()*(H-240)
	ang := s.rng.Float64()*0.6 - 0.3
	sp := float64(s.Settings.Speed)
	b.Speed = sp
	b.Spin = 0
	b.LastHit = 0
	s.Rally = 0
	vx := math.Cos(ang) * sp * float64(s.ServeDir)
	vy := math.Sin(ang)*sp + 60
	if s.rng.Float64() < 0.5 {
		vy = math.Sin(ang)*sp - 60
	}
	s.ServeCurrentDir = s.ServeDir
	s.ServeVX, s.ServeVY = vx, vy
	b.VX, b.VY = 0, 0
	s.ServePreviewUntil = now + 0.9
	s.ServeArmed = true
	s.ServeDir *= -1
	tot := s.P1.Score + s.P2.Score
	if tot > 0 && tot%3 == 0 {
		s.Wind = 1
		if s.rng.Float64() < 0.5 {
			s.Wind = -1
		}
	} else {
		s.Wind = 0
	}
	if s.Wind != 0 {
		s.EventID++
		s.LastPower = &PowerEv{Kind: "wind", By: 0, ID: s.EventID}
	}
}

func (s *Sim) SpawnPowerup(now float64) {
	s.Powerup = &Powerup{
		X: W*0.3 + s.rng.Float64()*W*0.4,
		Y: 80 + s.rng.Float64()*(H-160),
		Kind: powerupKinds[s.rng.Intn(len(powerupKinds))],
		Born: now,
	}
}

func (s *Sim) ApplyPowerup(kind string, hitter int, now float64) {
	var me, op *Paddle
	if hitter == 1 {
		me, op = s.P1, s.P2
	} else {
		me, op = s.P2, s.P1
	}
	b := s.Ball
	switch kind {
	case "expand":
		me.H = 140
		me.EffectUntil = now + 10
	case "shrink":
		op.H = 52
		op.EffectUntil = now + 10
	case "turbo":
		b.VX *= 1.45
		b.VY *= 1.45
		b.Speed = math.Min(900, b.Speed*1.2)
	case "slow":
		b.VX *= 0.65
		b.VY *= 0.65
		b.Speed = math.Max(280, b.Speed*0.85)
	case "shield":
		if pl := s.Players[hitter-1]; pl != nil {
			pl.Shield = true
		}
	case "freeze":
		idx := 1
		if hitter != 1 {
			idx = 0
		}
		if foe := s.Players[idx]; foe != nil {
			foe.FrozenUntil = now + 2.5
		}
	case "magnet":
		if pl := s.Players[hitter-1]; pl != nil {
			pl.MagnetUntil = now + 8
		}
	case "ghost":
		s.GhostUntil = now + 2.5
		if hitter == 1 {
			s.GhostHiddenFor = 2
		} else {
			s.GhostHiddenFor = 1
		}
	case "swap":
		s.P1.Y, s.P2.Y = s.P2.Y, s.P1.Y
		for i := range 2 {
			if pl := s.Players[i]; pl != nil {
				key := s.P1
				if i == 1 {
					key = s.P2
				}
				pl.Y, pl.PrevY, pl.VY = key.Y, key.Y, 0
			}
		}
	case "vortex":
		s.EventID++
		vx := math.Max(120, math.Min(W-120, b.X))
		vy := math.Max(80, math.Min(H-80, b.Y))
		s.Vortex = &Vortex{X: vx, Y: vy, Until: now + 4.5, ID: s.EventID}
	}
	s.EventID++
	s.LastPower = &PowerEv{Kind: kind, By: hitter, ID: s.EventID}
}

func (s *Sim) Point(winner int, now float64) {
	if winner == 2 && s.Players[0] != nil && s.Players[0].Shield {
		s.Players[0].Shield = false
		s.EventID++
		s.LastBlock = &BlockEv{By: 1, ID: s.EventID}
		s.Phase = "point"
		s.PointScored = 0
		s.PointEnd = now + 1.0
		s.Powerup = nil
		return
	}
	if winner == 1 && s.Players[1] != nil && s.Players[1].Shield {
		s.Players[1].Shield = false
		s.EventID++
		s.LastBlock = &BlockEv{By: 2, ID: s.EventID}
		s.Phase = "point"
		s.PointScored = 0
		s.PointEnd = now + 1.0
		s.Powerup = nil
		return
	}
	if winner == 1 {
		s.P1.Score++
	} else {
		s.P2.Score++
	}
	s.Rally = 0
	s.Powerup = nil
	s.EventID++
	s.LastPoint = &PointEv{Scored: winner,
		Scores: [2]int{s.P1.Score, s.P2.Score}, ID: s.EventID}
	win := s.Settings.Win
	a, b := s.P1.Score, s.P2.Score
	if ((a >= win || b >= win) && abs(a-b) >= 2) || a >= win+4 || b >= win+4 {
		s.Phase = "over"
		s.Winner = winner
	} else {
		s.Phase = "point"
		s.PointScored = winner
		s.PointEnd = now + 1.2
		if s.P1.Score >= win-1 && s.P2.Score >= win-1 {
			s.Sudden = true
			s.EventID++
			s.LastPower = &PowerEv{Kind: "sudden", By: 0, ID: s.EventID}
		}
	}
}

func (s *Sim) ObstacleUpdate(now float64) {
	if !s.Settings.Obstacle {
		s.Obstacle = nil
		return
	}
	ob := s.Obstacle
	if ob == nil {
		if now >= s.ObNext {
			s.Obstacle = &Obstacle{
				X: W/2 + (s.rng.Float64()-0.5)*140,
				Y: H/2 + (s.rng.Float64()-0.5)*140,
				Ang: s.rng.Float64() * math.Pi,
				Len: 110 + s.rng.Float64()*70,
				Phase: "warn_in",
				Until: now + 1.2,
			}
		}
		return
	}
	if now >= ob.Until {
		switch ob.Phase {
		case "warn_in":
			ob.Phase = "active"
			ob.Until = now + 6 + s.rng.Float64()*3
		case "active":
			ob.Phase = "warn_out"
			ob.Until = now + 0.8
		case "warn_out":
			s.Obstacle = nil
			s.ObNext = now + 8 + s.rng.Float64()*6
		}
	}
}

func (s *Sim) ObstacleCollide() {
	ob := s.Obstacle
	if ob == nil || ob.Phase != "active" {
		return
	}
	b := s.Ball
	dx, dy := math.Cos(ob.Ang), math.Sin(ob.Ang)
	hl := ob.Len / 2
	rx, ry := b.X-ob.X, b.Y-ob.Y
	proj := math.Max(-hl, math.Min(hl, rx*dx+ry*dy))
	cx, cy := ob.X+dx*proj, ob.Y+dy*proj
	nx, ny := b.X-cx, b.Y-cy
	dist := math.Hypot(nx, ny)
	const R = 9.0
	if dist < R+2 {
		if dist < 1e-6 {
			nx, ny = -dy, dx
		} else {
			nx /= dist
			ny /= dist
		}
		dot := b.VX*nx + b.VY*ny
		if dot < 0 {
			b.VX -= 2 * dot * nx
			b.VY -= 2 * dot * ny
			sp := math.Hypot(b.VX, b.VY)
			if sp == 0 {
				sp = 1
			}
			tgt := math.Min(920, b.Speed)
			b.VX *= tgt / sp
			b.VY *= tgt / sp
			b.X = cx + nx*(R+3)
			b.Y = cy + ny*(R+3)
			b.Spin = 0
			s.EventID++
			s.LastBounce = &BounceEv{ID: s.EventID}
		}
	}
}

func (s *Sim) BotUpdate(dt, now float64) {
	if !s.Bot {
		return
	}
	b := s.Ball
	target := 0.5
	if b.VX > 50 {
		tHit := ((W - 30 - 14) - b.X) / b.VX
		if tHit > 0 {
			py := b.Y + b.VY*math.Min(tHit, 3)
			span := H - 20
			m := math.Mod(py-10, 2*span)
			if m < 0 {
				m += 2 * span
			}
			if m <= span {
				py = 10 + m
			} else {
				py = 10 + 2*span - m
			}
			if s.rng.Float64() < 0.03 {
				s.BotErr = (s.rng.Float64() - 0.5) * 0.14
			}
			target = math.Max(0, math.Min(1, py/H+s.BotErr))
		}
	}
	cur := s.P2.Y
	maxstep := 1.15 * dt
	d := math.Max(-maxstep, math.Min(maxstep, target-cur))
	s.P2.Y = math.Max(0, math.Min(1, cur+d))
	den := now - s.BotPrevT
	if den < 1e-3 {
		den = 1e-3
	}
	inst := d / den
	s.BotVY = s.BotVY*0.6 + math.Max(-4, math.Min(4, inst))*0.4
	s.BotPrevY = s.P2.Y
	s.BotPrevT = now
}

// EnterCountdown performs the auto-start init block from game_loop.
func (s *Sim) EnterCountdown(now float64) {
	s.Phase = "countdown"
	s.CountdownEnd = now + 2.4
	s.ResetScores()
	s.ResetPositions()
	s.ResetScores()
	s.ResetPositions()
	for _, p := range [2]*Player{s.Players[0], s.Players[1]} {
		if p == nil {
			continue
		}
		p.Shield = false
		p.FrozenUntil = 0
		p.OdUntil = 0
		p.OdReadyAt = 0
		p.VY = 0
		p.MagnetUntil = 0
	}
	s.BotVY = 0
	s.BotErr = 0
	s.BotPrevY = 0.5
	s.GhostUntil = 0
	s.GhostHiddenFor = 0
	s.Vortex = nil
	s.Ball.Speed = float64(s.Settings.Speed)
	s.Ball.LastHit = 0
	s.Ball.Spin = 0
	s.Wind = 0
	s.Obstacle = nil
	s.ObNext = now + 6
	s.LastBounce = nil
	if s.rng.Float64() < 0.5 {
		s.ServeDir = -1
	} else {
		s.ServeDir = 1
	}
	s.Powerup = nil
	s.PowerupTimer = 5
}

// ShouldStart mirrors the auto-start condition in game_loop.
func (s *Sim) ShouldStart() bool {
	p0, p1 := s.Players[0], s.Players[1]
	foeReady := (p1 != nil && p1.Ready) || (s.Bot && p1 == nil)
	return s.Phase == "waiting" && p0 != nil && p0.Ready && foeReady
}

// Step advances the simulation by dt. now is the current unix time.
func (s *Sim) Step(dt, now float64) {
	if s.Phase == "countdown" && now >= s.CountdownEnd {
		s.Phase = "playing"
		s.Serve(now)
		return
	}
	if s.Phase == "point" && now >= s.PointEnd {
		s.Phase = "playing"
		if s.PointScored == 1 {
			s.ServeDir = -1
		} else {
			s.ServeDir = 1
		}
		s.Serve(now)
		return
	}
	if s.Phase != "playing" {
		return
	}
	s.ObstacleUpdate(now)
	s.BotUpdate(dt, now)
	if s.ServeArmed {
		if now < s.ServePreviewUntil {
			s.Ball.VX, s.Ball.VY = 0, 0
			return
		}
		s.Ball.VX, s.Ball.VY = s.ServeVX, s.ServeVY
		s.ServeArmed = false
	}
	pads := [2]*Paddle{s.P1, s.P2}
	for i := range 2 {
		pl := s.Players[i]
		base := BasePaddleH
		if s.Sudden {
			base = 64
		}
		if pl != nil && now < pl.OdUntil {
			pads[i].H += (base*1.6 - pads[i].H) * 0.2
		} else if now > pads[i].EffectUntil {
			pads[i].H += (base - pads[i].H) * 0.05
		}
	}
	if s.Sudden {
		s.Ball.VX *= 1.0015
		s.Ball.VY *= 1.0015
	}
	b := s.Ball
	b.X += b.VX * dt
	b.Y += b.VY * dt
	if s.Wind != 0 {
		b.VY += float64(s.Wind) * WindAccel * dt
	}
	b.VY += b.Spin * dt * 120
	b.Spin *= 0.99
	if b.Y < 10 {
		b.Y = 10
		b.VY = math.Abs(b.VY)
	} else if b.Y > H-10 {
		b.Y = H - 10
		b.VY = -math.Abs(b.VY)
	}
	p1x, p2x := 30.0, W-30-14
	p1ypx, p2ypx := s.P1.Y*H, s.P2.Y*H
	var p1v float64
	if s.Players[0] != nil {
		p1v = s.Players[0].VY
	}
	var p2v float64
	if s.Bot {
		p2v = s.BotVY
	} else if s.Players[1] != nil {
		p2v = s.Players[1].VY
	}
	if s.Players[0] != nil && now < s.Players[0].MagnetUntil && b.X < W/2 && b.VX < 0 {
		b.VY += math.Max(-1, math.Min(1, (s.P1.Y*H-b.Y)/60)) * 550 * dt
	}
	if s.Players[1] != nil && now < s.Players[1].MagnetUntil && b.X > W/2 && b.VX > 0 {
		b.VY += math.Max(-1, math.Min(1, (s.P2.Y*H-b.Y)/60)) * 550 * dt
	}
	if vx := s.Vortex; vx != nil && now < vx.Until {
		dx, dy := vx.X-b.X, vx.Y-b.Y
		dist := math.Hypot(dx, dy)
		if dist < 16 {
			sp := math.Min(920, math.Hypot(b.VX, b.VY)*1.25+40)
			b.Speed = sp
			nx, ny := 1.0, 0.0
			if dist > 1e-6 {
				nx, ny = dx/dist, dy/dist
			}
			b.VX = -nx*sp*0.7 + b.VX*0.3
			b.VY = -ny*sp*0.7 + b.VY*0.3
			n2 := math.Hypot(b.VX, b.VY)
			if n2 == 0 {
				n2 = 1
			}
			b.VX *= sp / n2
			b.VY *= sp / n2
			b.X = vx.X - nx*24
			b.Y = vx.Y - ny*24
		} else if dist < 320 {
			pull := math.Min(1400, 90000/math.Max(dist, 30))
			b.VX += dx / dist * pull * dt
			b.VY += dy / dist * pull * dt
			sp := math.Hypot(b.VX, b.VY)
			cap := math.Min(920, b.Speed*1.35)
			if sp > cap {
				b.VX *= cap / sp
				b.VY *= cap / sp
			}
		}
	} else if vx != nil {
		s.Vortex = nil
	}
	if b.VX < 0 && b.X-8 < p1x+14 && b.X-8 > p1x-12 && math.Abs(b.Y-p1ypx) < s.P1.H/2+8 {
		b.X = p1x + 14 + 8
		rel := (b.Y - p1ypx) / (s.P1.H/2 + 1e-6)
		bounce := math.Max(-1, math.Min(1, rel)) * 0.9
		sp := math.Min(920, math.Hypot(b.VX, b.VY)*1.045+8)
		b.Speed = sp
		b.VX = math.Cos(bounce) * sp
		b.VY = math.Sin(bounce)*sp + p1v*H*0.12
		b.LastHit = 1
		s.Rally++
		b.Spin = math.Max(-0.5, math.Min(0.5, p1v*0.7))
	}
	if b.VX > 0 && b.X+8 > p2x && b.X+8 < p2x+14+12 && math.Abs(b.Y-p2ypx) < s.P2.H/2+8 {
		b.X = p2x - 8
		rel := (b.Y - p2ypx) / (s.P2.H/2 + 1e-6)
		bounce := math.Max(-1, math.Min(1, rel)) * 0.9
		sp := math.Min(920, math.Hypot(b.VX, b.VY)*1.045+8)
		b.Speed = sp
		b.VX = -math.Cos(bounce) * sp
		b.VY = math.Sin(bounce)*sp + p2v*H*0.12
		b.LastHit = 2
		s.Rally++
		b.Spin = math.Max(-0.5, math.Min(0.5, p2v*0.7))
	}
	s.ObstacleCollide()
	if s.Powerup != nil && b.LastHit != 0 {
		dx, dy := b.X-s.Powerup.X, b.Y-s.Powerup.Y
		if dx*dx+dy*dy < 28*28 {
			s.ApplyPowerup(s.Powerup.Kind, b.LastHit, now)
			s.Powerup = nil
			s.PowerupTimer = 4 + s.rng.Float64()*4
		}
	} else {
		s.PowerupTimer -= dt
		if s.PowerupTimer <= 0 && s.Powerup == nil {
			s.SpawnPowerup(now)
			s.PowerupTimer = 6 + s.rng.Float64()*5
		}
	}
	if s.Powerup != nil && now-s.Powerup.Born > 12 {
		s.Powerup = nil
		s.PowerupTimer = 3
	}
	if b.X < -20 {
		s.Point(2, now)
	} else if b.X > W+20 {
		s.Point(1, now)
	}
}

// ShiftPaused thaws every absolute deadline by d (presence timers untouched).
// Mirrors the Python quirk: paddle effect_until is NOT shifted.
func (s *Sim) ShiftPaused(d float64) {
	s.CountdownEnd += d
	s.PointEnd += d
	s.ServePreviewUntil += d
	s.ObNext += d
	s.GhostUntil += d
	if s.Obstacle != nil && s.Obstacle.Until != 0 {
		s.Obstacle.Until += d
	}
	if s.Vortex != nil && s.Vortex.Until != 0 {
		s.Vortex.Until += d
	}
	if s.Powerup != nil && s.Powerup.Born != 0 {
		s.Powerup.Born += d
	}
	for _, p := range s.Players {
		if p == nil {
			continue
		}
		if p.FrozenUntil != 0 {
			p.FrozenUntil += d
		}
		if p.MagnetUntil != 0 {
			p.MagnetUntil += d
		}
		if p.OdUntil != 0 {
			p.OdUntil += d
		}
		if p.OdReadyAt != 0 {
			p.OdReadyAt += d
		}
	}
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func r1(v float64) float64 { return math.Round(v*10) / 10 }
