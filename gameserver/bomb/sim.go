// Bomb Tag server port: LAN 2-4 player hot potato, authoritative sim.
// Wire protocol is byte-compatible with server.py (same /api/*, same JSON).
package bomb

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

const (
	Version    = "1.29"
	WinRounds  = 5
	MaxSeats   = 4
	BaseW      = 800
	BaseH      = 560
	RunR       = 15.0
	RunSpeed   = 235.0
	HolderSpd  = 210.0
	DashMult   = 2.3
	DashTime   = 0.28
	DashCD     = 3.0
	TagDist    = 36.0
	TagImm     = 1.0
	PadR       = 30.0
	BoostTime  = 0.55
	BoostMult  = 1.7
	SlingSpeed = 650.0
	SlingTime  = 0.45
	MaxPads    = 2
	PadLife    = 10.0
)

type pillar struct{ X, Y, W, H float64 }
type spawn struct{ X, Y float64 }

var pillarsBase = []pillar{
	{200, 140, 60, 60}, {540, 140, 60, 60},
	{200, 360, 60, 60}, {540, 360, 60, 60},
	{370, 250, 60, 60},
}
var spawnsBase = []spawn{{100, 280}, {700, 280}, {100, 100}, {700, 460}}

type player struct {
	ID       string
	Name     string
	LastSeen time.Time
	Ready    bool
	IX, IY   float64
	Fire     bool
	PrevFire bool
}

type pos struct{ X, Y, Score float64 }
type pad struct {
	X, Y, DX, DY float64
	Expires      time.Time
	Dead         bool
}

type gameT struct {
	mu          sync.Mutex
	phase       string
	countEnd    time.Time
	roundEnd    time.Time
	round       int
	players     [MaxSeats]*player
	pos         [MaxSeats]pos
	dashUntil   [MaxSeats]time.Time
	dashCD      [MaxSeats]time.Time
	immUntil    [MaxSeats]time.Time
	holder      int
	fuse        float64
	pads        []pad
	padTimer    float64
	boostUntil  [MaxSeats]time.Time
	slingDX     [MaxSeats]float64
	slingDY     [MaxSeats]float64
	slingUntil  [MaxSeats]time.Time
	lastBoost   map[string]any
	winner      int
	eventID     int
	lastPass    map[string]any
	lastBoom    map[string]any
	paused      bool
	pausedBy    string
	pausedSince time.Time
	aw, ah      float64
	pillars     []pillar
	spawns      []spawn
	nstart      int
	blasts      int
	out         [MaxSeats]bool
	loser       int
	rng         *rand.Rand
}

func newGame() *gameT {
	return &gameT{phase: "waiting", round: 1, rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

func (g *gameT) occupied() []int {
	var o []int
	for i, p := range g.players {
		if p != nil {
			o = append(o, i)
		}
	}
	return o
}
func (g *gameT) alive() []int {
	var o []int
	for i, p := range g.players {
		if p != nil && !g.out[i] {
			o = append(o, i)
		}
	}
	return o
}

func arenaFor(n int) (float64, float64) {
	if n >= 3 {
		return 960, 600
	}
	return 800, 560
}

func (g *gameT) applyArena(n int) {
	aw, ah := arenaFor(n)
	g.aw, g.ah = aw, ah
	sx, sy := aw/800, ah/560
	g.pillars = nil
	for _, p := range pillarsBase {
		w := math.Max(40, math.Round(p.W*sx))
		h := math.Max(40, math.Round(p.H*sy))
		g.pillars = append(g.pillars, pillar{math.Round(p.X * sx), math.Round(p.Y * sy), w, h})
	}
	g.spawns = nil
	for _, s := range spawnsBase {
		x := math.Min(aw-60, math.Max(60, s.X*sx))
		y := math.Min(ah-60, math.Max(60, s.Y*sy))
		g.spawns = append(g.spawns, spawn{math.Round(x), math.Round(y)})
	}
}

func (g *gameT) resetPositions() {
	sps := g.spawns
	if len(sps) == 0 {
		sps = spawnsBase
	}
	for i, s := range sps {
		if i >= MaxSeats {
			break
		}
		g.pos[i].X, g.pos[i].Y = s.X, s.Y
	}
	var z time.Time
	for i := 0; i < MaxSeats; i++ {
		g.immUntil[i] = z
		g.dashUntil[i] = z
		g.dashCD[i] = z
		g.boostUntil[i] = z
		g.slingDX[i], g.slingDY[i] = 0, 0
		g.slingUntil[i] = z
	}
	g.pads = nil
	g.padTimer = 2.5
}

func (g *gameT) spawnPad() {
	if len(g.pads) >= MaxPads {
		return
	}
	for k := 0; k < 30; k++ {
		x := 70 + g.rng.Float64()*(g.aw-140)
		y := 70 + g.rng.Float64()*(g.ah-140)
		bad := false
		for _, pl := range g.pillars {
			if pl.X-25 < x && x < pl.X+pl.W+25 && pl.Y-25 < y && y < pl.Y+pl.H+25 {
				bad = true
				break
			}
		}
		if bad {
			continue
		}
		ok := true
		for i := 0; i < MaxSeats; i++ {
			if math.Hypot(x-g.pos[i].X, y-g.pos[i].Y) <= 120 {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		a := g.rng.Float64() * 2 * math.Pi
		g.pads = append(g.pads, pad{X: x, Y: y, DX: math.Cos(a), DY: math.Sin(a), Expires: time.Now().Add(PadLife * time.Second)})
		return
	}
}

func (g *gameT) resetScores() {
	start := 0
	if len(g.occupied()) > 2 {
		start = 5
	}
	for i := range g.pos {
		g.pos[i].Score = float64(start)
	}
	g.winner, g.loser, g.round, g.blasts = 0, 0, 1, 0
	for i := range g.out {
		g.out[i] = false
	}
}

func (g *gameT) newRound() {
	g.resetPositions()
	occ := g.occupied()
	if len(occ) == 0 {
		occ = []int{0, 1} // nobody seated: keep seats 1-2 as the holder pool
	}
	pool := []int{}
	for _, i := range occ {
		if !g.out[i] {
			pool = append(pool, i+1)
		}
	}
	if len(pool) == 0 {
		for _, i := range occ {
			pool = append(pool, i+1)
		}
	}
	inh := false
	for _, h := range pool {
		if h == g.holder {
			inh = true
			break
		}
	}
	if !inh {
		g.holder = pool[g.rng.Intn(len(pool))]
	} else {
		others := []int{}
		for _, h := range pool {
			if h != g.holder {
				others = append(others, h)
			}
		}
		if len(others) == 0 {
			others = []int{g.holder}
		}
		g.holder = others[g.rng.Intn(len(others))]
	}
	g.fuse = 8 + g.rng.Float64()*6
	g.lastPass = nil
}

func (g *gameT) touch(pid string) {
	for _, p := range g.players {
		if p != nil && p.ID == pid {
			p.LastSeen = time.Now()
		}
	}
}
func (g *gameT) slotOf(pid string) int {
	for i, p := range g.players {
		if p != nil && p.ID == pid {
			return i
		}
	}
	return -1
}
func (g *gameT) freeStale() {
	for i, p := range g.players {
		if p != nil && time.Since(p.LastSeen) > 8*time.Second {
			g.players[i] = nil
		}
	}
}

func (g *gameT) collide(x, y float64) (float64, float64) {
	aw, ah := g.aw, g.ah
	x = math.Max(RunR, math.Min(aw-RunR, x))
	y = math.Max(RunR, math.Min(ah-RunR, y))
	for _, pl := range g.pillars {
		cx := math.Max(pl.X, math.Min(pl.X+pl.W, x))
		cy := math.Max(pl.Y, math.Min(pl.Y+pl.H, y))
		dx, dy := x-cx, y-cy
		d2 := dx*dx + dy*dy
		if d2 < RunR*RunR {
			if d2 > 1e-6 {
				d := math.Sqrt(d2)
				x, y = cx+dx/d*RunR, cy+dy/d*RunR
			} else {
				l, r, tp, b := x-pl.X, pl.X+pl.W-x, y-pl.Y, pl.Y+pl.H-y
				m := math.Min(math.Min(l, r), math.Min(tp, b))
				switch m {
				case l:
					x = pl.X - RunR
				case r:
					x = pl.X + pl.W + RunR
				case tp:
					y = pl.Y - RunR
				default:
					y = pl.Y + pl.H + RunR
				}
			}
		}
	}
	return x, y
}

func (g *gameT) explode() {
	h := g.holder - 1
	occ := g.occupied()
	g.eventID++
	g.lastBoom = map[string]any{
		"x": round1(g.pos[h].X), "y": round1(g.pos[h].Y),
		"scorer": 0, "id": g.eventID,
	}
	g.lastPass = nil
	if g.nstart <= 2 {
		hx, hy := g.pos[h].X, g.pos[h].Y
		scorer, bd := 1, -1.0
		for _, i := range occ {
			if i == h {
				continue
			}
			d := math.Hypot(hx-g.pos[i].X, hy-g.pos[i].Y)
			if bd < 0 || d < bd {
				scorer, bd = i+1, d
			}
		}
		g.pos[scorer-1].Score++
		g.lastBoom["scorer"] = scorer
		if g.pos[scorer-1].Score >= WinRounds {
			g.phase, g.winner = "over", scorer
		} else {
			g.phase, g.roundEnd = "round", time.Now().Add(2500*time.Millisecond)
		}
	} else {
		g.pos[h].Score--
		g.lastBoom["holder"] = h + 1
		g.blasts++
		if g.pos[h].Score <= 0 {
			g.pos[h].Score = 0
			g.out[h] = true
			g.loser = h + 1
			rest := g.alive()
			w := []int{}
			for _, i := range rest {
				w = append(w, i+1)
			}
			if len(w) == 1 {
				g.phase, g.winner = "over", w[0]
			} else if len(w) == 0 {
				g.phase, g.winner = "over", 0
			} else {
				g.phase, g.roundEnd = "round", time.Now().Add(2500*time.Millisecond)
			}
		} else {
			g.phase, g.roundEnd = "round", time.Now().Add(2500*time.Millisecond)
		}
	}
}

func (g *gameT) step(dt float64) {
	now := time.Now()
	if g.phase == "countdown" && !now.Before(g.countEnd) {
		g.phase = "playing"
		return
	}
	if g.phase == "round" && !now.Before(g.roundEnd) {
		g.round++
		g.applyArena(len(g.occupied()))
		g.newRound()
		g.phase = "playing"
		return
	}
	if g.phase != "playing" {
		return
	}
	t := now
	occ := g.occupied()
	if len(occ) > 0 {
		inh := false
		for _, i := range occ {
			if i == g.holder-1 {
				inh = true
				break
			}
		}
		if !inh {
			pool := []int{}
			for _, i := range occ {
				if !g.out[i] {
					pool = append(pool, i)
				}
			}
			if len(pool) == 0 {
				pool = occ
			}
			g.holder = pool[g.rng.Intn(len(pool))] + 1
		}
	}
	for i := 0; i < MaxSeats; i++ {
		if g.out[i] {
			continue
		}
		p := g.players[i]
		ix, iy := 0.0, 0.0
		if p != nil {
			ix = math.Max(-1, math.Min(1, p.IX))
			iy = math.Max(-1, math.Min(1, p.IY))
			if p.Fire && !p.PrevFire && !t.Before(g.dashCD[i]) {
				g.dashUntil[i] = t.Add(time.Duration(DashTime * float64(time.Second)))
				g.dashCD[i] = t.Add(time.Duration(DashCD * float64(time.Second)))
			}
			p.PrevFire = p.Fire
		}
		n := math.Hypot(ix, iy)
		if n > 1 {
			ix, iy = ix/n, iy/n
		}
		spd := RunSpeed
		if g.holder == i+1 {
			spd = HolderSpd
		}
		if t.Before(g.dashUntil[i]) {
			spd *= DashMult
		}
		if t.Before(g.boostUntil[i]) {
			// descending boost: 1.7x easing back to normal (holder or runner) speed
			left := g.boostUntil[i].Sub(t).Seconds() / BoostTime
			if left < 0 {
				left = 0
			}
			spd *= 1 + (BoostMult-1)*left
		}
		x, y := g.pos[i].X+ix*spd*dt, g.pos[i].Y+iy*spd*dt
		if t.Before(g.slingUntil[i]) {
			// slingshot fling along the pad arrow, decaying to zero
			left := g.slingUntil[i].Sub(t).Seconds() / SlingTime
			if left < 0 {
				left = 0
			}
			sv := SlingSpeed * left
			x += g.slingDX[i] * sv * dt
			y += g.slingDY[i] * sv * dt
		}
		g.pos[i].X, g.pos[i].Y = g.collide(x, y)
		g.padTimer -= dt
		if g.padTimer <= 0 {
			g.spawnPad()
			g.padTimer = 4 + g.rng.Float64()*3
		}
		for k := range g.pads {
			pd := &g.pads[k]
			if time.Now().After(pd.Expires) {
				pd.Dead = true
				continue
			}
			for j := 0; j < MaxSeats; j++ {
				if g.players[j] == nil {
					continue
				}
				if math.Hypot(g.pos[j].X-pd.X, g.pos[j].Y-pd.Y) < PadR {
					pd.Dead = true
					g.boostUntil[j] = t.Add(time.Duration(BoostTime * float64(time.Second)))
					g.slingDX[j], g.slingDY[j] = pd.DX, pd.DY
					g.slingUntil[j] = t.Add(time.Duration(SlingTime * float64(time.Second)))
					g.eventID++
					g.lastBoost = map[string]any{"x": pd.X, "y": pd.Y, "by": j + 1, "id": g.eventID}
					break
				}
			}
		}
		kept := g.pads[:0]
		for _, pd := range g.pads {
			if !pd.Dead {
				kept = append(kept, pd)
			}
		}
		g.pads = kept
	}
	h := g.holder - 1
	best, bd := -1, TagDist
	for _, i := range occ {
		if i == h || g.out[i] {
			continue
		}
		d := math.Hypot(g.pos[h].X-g.pos[i].X, g.pos[h].Y-g.pos[i].Y)
		if d < bd {
			best, bd = i, d
		}
	}
	if best >= 0 && !t.Before(g.immUntil[h]) {
		o := best
		g.holder = o + 1
		g.immUntil[o] = t.Add(time.Duration(TagImm * float64(time.Second)))
		dx := g.pos[o].X - g.pos[h].X
		dy := g.pos[o].Y - g.pos[h].Y
		dd := math.Hypot(dx, dy)
		if dd == 0 {
			dd = 1
		}
		nx, ny := g.collide(g.pos[h].X-dx/dd*24, g.pos[h].Y-dy/dd*24)
		g.pos[h].X, g.pos[h].Y = nx, ny
		g.eventID++
		g.lastPass = map[string]any{"holder": o + 1, "id": g.eventID}
	}
	g.fuse -= dt
	if g.fuse <= 0 {
		g.fuse = 0
		g.explode()
	}
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }
func round2(x float64) float64 { return math.Round(x*100) / 100 }
func round3(x float64) float64 { return math.Round(x*1000) / 1000 }
func fsec(d time.Duration) float64 {
	if d < 0 {
		return 0
	}
	return math.Round(float64(d)/float64(time.Second)*100) / 100
}

func (g *gameT) shiftPaused(d time.Duration) {
	g.countEnd = g.countEnd.Add(d)
	g.roundEnd = g.roundEnd.Add(d)
	shift := func(t time.Time) time.Time {
		if t.IsZero() {
			return t
		}
		return t.Add(d)
	}
	for i := 0; i < MaxSeats; i++ {
		g.dashUntil[i] = shift(g.dashUntil[i])
		g.dashCD[i] = shift(g.dashCD[i])
		g.immUntil[i] = shift(g.immUntil[i])
		g.boostUntil[i] = shift(g.boostUntil[i])
		g.slingUntil[i] = shift(g.slingUntil[i])
	}
	for k := range g.pads {
		g.pads[k].Expires = g.pads[k].Expires.Add(d)
	}
}
