// Package horde is a Go port of the Neon Horde LAN server (2players/neon-horde).
// Wire protocol and game logic are identical to server.py.
package horde

import (
	"math"
	"math/rand"
	"time"
)

const VERSION = "2.2"

const (
	MaxSeats   = 4
	ViewR      = 1350.0
	SpawnMin   = 950.0
	SpawnMax   = 1250.0
	DespawnR   = 2200.0
	GemDespawnR = 2600.0
	EnemyCap   = 110
	GemCap     = 170
	BossEvery  = 240.0
	EliteEvery = 45.0
	TierDist   = 4500.0
)

type DraftOpt struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
}

type Player struct {
	ID       string
	Name     string
	LastSeen float64
	Ready    bool
	IX, IY   float64
	X, Y     float64
	HP, MaxHP float64
	Level    int
	XP       int
	XPN      int
	Speed    float64
	Dmg      float64
	CDR      float64
	Armor    int
	Magnet   float64
	Lifesteal float64
	Thorns   int
	ReviveMult float64
	Wpn      map[string]int
	Arti     []string
	Evo      []string
	Pending  []*DraftOpt
	PendT    float64
	Down     bool
	Revive   float64
	Prot     float64
	Bleed    float64
	Dead     bool
	BladeA   float64
	FrostA   float64
	BoltT    float64
	NovaT    float64
	ChainT   float64
	MslT     float64
	AxeT     float64
	AcidT    float64
	Kills    int
}

type Enemy struct {
	X, Y       float64
	HP, MaxHP  float64
	Spd        float64
	Dmg        float64
	R          float64
	Kind       string
	Elite      bool
	Tier       int
	Boss       bool
	Atk        float64
	KX, KY     float64
	BladeHit   float64
	FrostHit   float64
	AxeHit     int
	Mode       string
	ModeT      float64
	DX, DY     float64
	SlowUntil  float64
	RestUntil  float64
	Despawn    float64
	Dead       bool
}

type Shot struct {
	X, Y     float64
	VX, VY   float64
	Dmg      float64
	Owner    int
	Life     float64
	Home     bool
	Aoe      float64
	Dead     bool
}

type Gem struct {
	X, Y float64
	V    int
	Dead bool
}

type Axe struct {
	X, Y     float64
	VX, VY   float64
	Owner    int
	Dmg      float64
	Dist     float64
	MaxD     float64
	Back     bool
	Throw    int
	Dead     bool
}

type Acid struct {
	X, Y  float64
	R     float64
	DPS   float64
	Owner int
	Until float64
	Tick  float64
}

type Fx struct {
	Kind  string
	Pts   any
	Until float64
}

type LvlEv struct {
	Seat int `json:"seat"`
	Lvl  int `json:"lvl"`
	ID   int `json:"id"`
}
type BossEv struct {
	N  int `json:"n"`
	ID int `json:"id"`
}
type IdEv struct {
	ID int `json:"id"`
}
type HurtEv struct {
	Seat int     `json:"seat"`
	Amt  float64 `json:"amt"`
	Fx   float64 `json:"fx"`
	Fy   float64 `json:"fy"`
	ID   int     `json:"id"`
}
type OverEv struct {
	Time  float64 `json:"time"`
	Kills int     `json:"kills"`
	ID    int     `json:"id"`
}

type Sim struct {
	Phase        string
	CountdownEnd float64
	Time         float64
	Players      [MaxSeats]*Player
	Enemies      []*Enemy
	Shots        []*Shot
	Gems         []*Gem
	Boss         *Enemy
	Axes         []*Axe
	Acids        []*Acid
	Fx           []*Fx
	ThrowN       int
	SpawnT       float64
	EliteT       float64
	BossT        float64
	BossN        int
	BloodUntil   float64
	BloodNext    float64
	Kills        int
	EventID      int
	LastLvl      *LvlEv
	LastBoss     *BossEv
	LastMoon     *IdEv
	LastGoblin   *IdEv
	LastHurt     *HurtEv
	LastOver     *OverEv
	Paused       bool
	PausedBy     string
	PausedSince  float64
	rng          *rand.Rand
}

func New() *Sim {
	return &Sim{
		Phase: "waiting",
		SpawnT: 1.0, EliteT: 30.0, BossT: BossEvery, BloodNext: 180.0,
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func unixNow() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func XpNext(lv int) int { return int(6 + lv*3) + int(float64(lv*lv)*0.2) }

func TierAt(x, y float64) int {
	t := int(math.Hypot(x, y) / TierDist)
	if t > 2 {
		t = 2
	}
	return t
}

func Mkbuild() *Player {
	return &Player{
		HP: 100, MaxHP: 100, Level: 1, XPN: XpNext(1),
		Speed: 250, Dmg: 1, CDR: 1, Magnet: 95, ReviveMult: 1,
		Wpn: map[string]int{}, Arti: []string{}, Evo: []string{},
	}
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

// --- draft ---

type statOpt struct{ id, name, desc string; w int }

var StatOpts = []statOpt{
	{"s_speed", "Swift Boots", "+8% move speed", 3},
	{"s_hp", "Vitality", "+20 max HP, heal 20", 3},
	{"s_dmg", "Power Core", "+12% damage", 3},
	{"s_cdr", "Overclock", "weapons fire 8% faster", 3},
	{"s_mag", "Magnet Coil", "+35% pickup radius", 2},
	{"s_armor", "Plating", "+1 armor (flat)", 2},
}
var ArtiOpts = []statOpt{
	{"a_life", "Lifesteal Fangs", "heal 4% of damage dealt", 2},
	{"a_thorn", "Thorn Mail", "attackers take 6", 2},
	{"a_medic", "Field Medic", "revive 40% faster, revives heal you 25", 2},
	{"a_wisdom", "Wisdom Charm", "+20% XP gained", 2},
}
var WpnNames = map[string]string{
	"w_blades": "Orbit Blades", "w_bolts": "Bolt Spitter", "w_nova": "Nova Pulse",
	"w_chain": "Chain Lightning", "w_frost": "Frost Orbitals", "w_msl": "Homing Missiles",
	"w_axe": "Boomerang Axe", "w_acid": "Acid Pools",
}
var WpnAll = []string{"w_blades", "w_bolts", "w_nova", "w_chain", "w_frost", "w_msl", "w_axe", "w_acid"}

type evoDef struct{ art, eid, name, desc string }

var Evo = map[string]evoDef{
	"w_blades": {"a_life", "e_reaper", "Soul Reaper", "blades execute under 15% HP"},
	"w_bolts": {"a_wisdom", "e_oracle", "Oracle Barrage", "bolts seek targets"},
	"w_nova": {"a_thorn", "e_thornova", "Thorn Nova", "huge, slows 2s"},
}

func hasStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Sim) DraftOptions(b *Player) []*DraftOpt {
	type cand struct{ id, name, desc string; w int }
	var pool []cand
	for _, w := range WpnAll {
		if ev, ok := Evo[w]; ok {
			if b.Wpn[w] >= 5 && hasStr(b.Arti, ev.art) && !hasStr(b.Evo, ev.eid) {
				pool = append(pool, cand{ev.eid, ev.name, ev.desc + " (EVOLVE)", 5})
				continue
			}
		}
		if _, ok := b.Wpn[w]; !ok {
			pool = append(pool, cand{w, WpnNames[w] + " NEW", "a new auto-weapon", 3})
		} else if b.Wpn[w] < 5 {
			pool = append(pool, cand{w, WpnNames[w] + " +" + itoa(b.Wpn[w]+1), "upgrade to Lv" + itoa(b.Wpn[w]+1), 3})
		}
	}
	for _, o := range StatOpts {
		if o.id == "s_cdr" && b.CDR <= 0.55 {
			continue
		}
		pool = append(pool, cand{o.id, o.name, o.desc, o.w})
	}
	for _, o := range ArtiOpts {
		if !hasStr(b.Arti, o.id) {
			pool = append(pool, cand{o.id, o.name, o.desc, o.w})
		}
	}
	if len(pool) == 0 {
		return []*DraftOpt{{ID: "s_heal", Name: "Repair", Desc: "heal 40"}}
	}
	var picks []*DraftOpt
	bag := append([]cand{}, pool...)
	for range min(3, len(bag)) {
		tot := 0
		for _, p := range bag {
			tot += p.w
		}
		r := s.rng.Float64() * float64(tot)
		acc := 0
		for k, p := range bag {
			acc += p.w
			if r <= float64(acc) {
				picks = append(picks, &DraftOpt{ID: p.id, Name: p.name, Desc: p.desc})
				bag = append(bag[:k], bag[k+1:]...)
				break
			}
		}
	}
	isWpn := func(id string) bool {
		return len(id) > 2 && (id[:2] == "w_" || id[:2] == "e_")
	}
	if len(picks) > 0 {
		has := false
		for _, o := range picks {
			if isWpn(o.ID) {
				has = true
				break
			}
		}
		if !has {
			var wpool []cand
			for _, p := range pool {
				if isWpn(p.id) {
					wpool = append(wpool, p)
				}
			}
			if len(wpool) > 0 {
				w := wpool[s.rng.Intn(len(wpool))]
				picks[len(picks)-1] = &DraftOpt{ID: w.id, Name: w.name, Desc: w.desc}
			}
		}
	}
	var elig []cand
	for _, p := range pool {
		if len(p.id) > 2 && p.id[:2] == "e_" {
			dup := false
			for _, o := range picks {
				if o.ID == p.id {
					dup = true
					break
				}
			}
			if !dup {
				elig = append(elig, p)
			}
		}
	}
	if len(elig) > 0 {
		has := false
		for _, o := range picks {
			if len(o.ID) > 2 && o.ID[:2] == "e_" {
				has = true
				break
			}
		}
		if !has {
			w := elig[s.rng.Intn(len(elig))]
			picks[len(picks)-1] = &DraftOpt{ID: w.id, Name: w.name, Desc: w.desc}
		}
	}
	return picks
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func (s *Sim) ApplyPick(i int, oid string) {
	b := s.Players[i]
	if oid == "s_heal" || oid == "" {
		b.HP = math.Min(b.MaxHP, b.HP+40)
		return
	}
	if _, ok := WpnNames[oid]; ok {
		b.Wpn[oid]++
		return
	}
	for _, ev := range Evo {
		if oid == ev.eid {
			b.Evo = append(b.Evo, ev.eid)
			return
		}
	}
	switch oid {
	case "s_speed":
		b.Speed *= 1.08
	case "s_hp":
		b.MaxHP += 20
		b.HP = math.Min(b.MaxHP, b.HP+20)
	case "s_dmg":
		b.Dmg *= 1.12
	case "s_cdr":
		b.CDR = math.Max(0.55, b.CDR*0.92)
	case "s_mag":
		b.Magnet *= 1.35
	case "s_armor":
		b.Armor++
	case "a_life":
		b.Lifesteal = 0.04
		b.Arti = append(b.Arti, oid)
	case "a_thorn":
		b.Thorns = 6
		b.Arti = append(b.Arti, oid)
	case "a_medic":
		b.ReviveMult = 1.4
		b.Arti = append(b.Arti, oid)
	case "a_wisdom":
		b.Arti = append(b.Arti, oid)
	}
}

func (s *Sim) EmitFx(kind string, pts any, now float64) {
	s.Fx = append(s.Fx, &Fx{Kind: kind, Pts: pts, Until: now + 0.45})
	if len(s.Fx) > 24 {
		s.Fx = s.Fx[1:]
	}
}

func (s *Sim) NearestEnemy(x, y, maxd float64, skip *Enemy) *Enemy {
	var best *Enemy
	bd := maxd
	for _, e := range s.Enemies {
		if e.Dead || e == skip {
			continue
		}
		if d := math.Hypot(e.X-x, e.Y-y); d < bd {
			best, bd = e, d
		}
	}
	return best
}

func (s *Sim) GainXp(i, amt int, now float64) {
	b := s.Players[i]
	if hasStr(b.Arti, "a_wisdom") {
		amt = int(float64(amt) * 1.2)
	}
	if now < s.BloodUntil {
		amt *= 2
	}
	b.XP += amt
	for b.XP >= b.XPN && b.Pending == nil {
		b.XP -= b.XPN
		b.Level++
		b.XPN = XpNext(b.Level)
		b.Pending = s.DraftOptions(b)
		b.PendT = now + 30
		s.EventID++
		s.LastLvl = &LvlEv{Seat: i + 1, Lvl: b.Level, ID: s.EventID}
	}
}

// --- enemies ---

func (s *Sim) HpScale() float64 { return 1 + s.Time/60*0.30 }

func (s *Sim) DmgScale() float64 { return 1 + s.Time/60*0.12 }

func (s *Sim) SpawnEnemy(elite, boss bool, now float64) *Enemy {
	_ = now
	occ := s.Occupied()
	ax, ay := 0.0, 0.0
	if len(occ) > 0 {
		a := s.Players[occ[s.rng.Intn(len(occ))]]
		ax, ay = a.X, a.Y
	}
	a := s.rng.Float64() * 2 * math.Pi
	if boss {
		x, y := ax+math.Cos(a)*1100, ay+math.Sin(a)*1100
		n := s.BossN
		hp := 1500 * (1 + float64(n)*0.9) * s.HpScale()
		e := &Enemy{X: x, Y: y, HP: hp, MaxHP: hp, Spd: 72,
			Dmg: 26 * s.DmgScale(), R: 34, Kind: "boss", Tier: 1, Boss: true}
		s.Boss = e
		return e
	}
	d := SpawnMin + s.rng.Float64()*(SpawnMax-SpawnMin)
	x, y := ax+math.Cos(a)*d, ay+math.Sin(a)*d
	tier := TierAt(x, y)
	t := s.Time
	r := s.rng.Float64()
	kind := "chaser"
	switch {
	case r > 0.90:
		kind = "brute"
	case r > 0.82 && t > 90:
		kind = "charger"
	case r > 0.74 && t > 60:
		kind = "splitter"
	case r > 0.62:
		kind = "darter"
	}
	base := map[string][4]float64{
		"chaser": {22, 96, 8, 13}, "darter": {12, 152, 6, 10},
		"brute": {72, 58, 14, 19}, "splitter": {30, 82, 8, 14},
		"charger": {46, 66, 12, 15},
	}[kind]
	hp, spd, dmg, rr := base[0], base[1], base[2], base[3]
	hp *= 1 + float64(tier)*0.6
	dmg *= 1 + float64(tier)*0.3
	if elite {
		hp *= 7
		spd *= 1.25
		dmg *= 1.5
		rr += 4
	}
	hs := hp * s.HpScale()
	return &Enemy{X: x, Y: y, HP: hs, MaxHP: hs, Spd: spd,
		Dmg: dmg * s.DmgScale(), R: rr, Kind: kind, Elite: elite,
		Tier: tier, Mode: "chase"}
}

func (s *Sim) SpawnGoblin(now float64) *Enemy {
	occ := s.Occupied()
	ax, ay := 0.0, 0.0
	if len(occ) > 0 {
		a := s.Players[occ[s.rng.Intn(len(occ))]]
		ax, ay = a.X, a.Y
	}
	a := s.rng.Float64() * 2 * math.Pi
	hp := 40 * s.HpScale()
	return &Enemy{X: ax + math.Cos(a)*1100, Y: ay + math.Sin(a)*1100,
		HP: hp, MaxHP: hp, Spd: 205, R: 12, Kind: "goblin",
		Tier: 1, Mode: "flee", Despawn: now + 20}
}

func GemValue(tier int, elite bool) int {
	if elite {
		return 3 + tier
	}
	return 1 + tier
}

func SegCircle(x1, y1, x2, y2, cx, cy, r float64) bool {
	dx, dy := x2-x1, y2-y1
	l2 := dx*dx + dy*dy
	t := 0.0
	if l2 != 0 {
		t = math.Max(0, math.Min(1, ((cx-x1)*dx+(cy-y1)*dy)/l2))
	}
	px, py := x1+dx*t, y1+dy*t
	return (cx-px)*(cx-px)+(cy-py)*(cy-py) < r*r
}

func (s *Sim) HurtEnemy(e *Enemy, dmg float64, owner int, now float64) {
	b := s.Players[owner]
	if !e.Boss && !e.Elite && hasStr(b.Evo, "e_reaper") {
		if e.HP/e.MaxHP < 0.15 {
			dmg = math.Max(dmg, e.MaxHP)
		}
	}
	e.HP -= dmg
	if e.HP <= 0 && !e.Dead {
		e.Dead = true
		s.Kills++
		if e.Kind == "splitter" && len(s.Enemies) < EnemyCap+10 {
			for _, sgn := range []float64{-1, 1} {
				hp := 14 * s.HpScale()
				s.Enemies = append(s.Enemies, &Enemy{X: e.X + sgn*16, Y: e.Y,
					HP: hp, MaxHP: hp, Spd: 110, Dmg: 6 * s.DmgScale(),
					R: 10, Kind: "chaser", Tier: e.Tier, Mode: "chase"})
			}
		}
		b = s.Players[owner]
		b.Kills++
		if b.Lifesteal != 0 {
			b.HP = math.Min(b.MaxHP, b.HP+dmg*b.Lifesteal)
		}
		nv := 1
		if e.Elite {
			nv = 8
		}
		for range nv {
			s.Gems = append(s.Gems, &Gem{
				X: e.X + (s.rng.Float64()*28 - 14),
				Y: e.Y + (s.rng.Float64()*28 - 14),
				V: GemValue(e.Tier, e.Elite)})
		}
		if e.Boss {
			s.Boss = nil
			for range 20 {
				s.Gems = append(s.Gems, &Gem{
					X: e.X + (s.rng.Float64()*60 - 30),
					Y: e.Y + (s.rng.Float64()*60 - 30), V: 3})
			}
			for _, i := range s.Occupied() {
				pb := s.Players[i]
				pb.HP = math.Min(pb.MaxHP, pb.HP+30)
			}
		}
	}
	_ = now
}

func (s *Sim) HurtPlayer(i int, dmg float64, now float64) {
	b := s.Players[i]
	if b.Down || b.Pending != nil || now < b.Prot {
		return
	}
	dmg = math.Max(1, dmg-float64(b.Armor))
	b.HP -= dmg
	if b.HP <= 0 {
		b.HP = 0
		b.Down = true
		b.Revive = 0
		b.Bleed = 25.0
		b.Pending = nil
	}
}
