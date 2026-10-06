// Deterministic bastion sim tests: fixed clock, direct state setup.
package bastion

import (
	"math"
	"testing"
)

func testSim() *Sim {
	s := New()
	s.Players[0] = &Player{ID: "a", Name: "A", LastSeen: 1000, Ready: true, X: 120, Y: 540}
	s.Players[1] = &Player{ID: "b", Name: "B", LastSeen: 1000, Ready: true, X: 240, Y: 540}
	return s
}

func startBuild(s *Sim, now float64) {
	s.EnterCountdown(now)
	s.Step(0.01, s.CountdownEnd+0.01) // countdown -> build
	if s.Phase != "build" {
		panic("no build phase")
	}
}

func TestPathWaypoints(t *testing.T) {
	if len(Path) == 0 || len(Waypts) != len(Path) || len(SegLens) != len(Path)-1 {
		t.Fatal("waypoint tables inconsistent")
	}
	// starts top-left, ends bottom-right area
	if Waypts[0].X != 30 || Waypts[0].Y != 90 {
		t.Fatalf("path start: %v", Waypts[0])
	}
	last := Waypts[len(Waypts)-1]
	if last.X != 15*Cell+Cell/2 || last.Y != 7*Cell+Cell/2 {
		t.Fatalf("path end: %v", last)
	}
	for _, l := range SegLens {
		if l <= 0 {
			t.Fatal("zero-length segment")
		}
	}
}

func TestBuildRepairPricing(t *testing.T) {
	s := testSim()
	now := 1000.0
	startBuild(s, now)
	if s.Gold != 90 || s.Base != MaxBase || s.Level != 1 {
		t.Fatalf("match setup wrong: gold=%d base=%d lvl=%d", s.Gold, s.Base, s.Level)
	}
	// stand on free grass (0,0 corner is grass, not path)
	p := s.Players[0]
	p.X, p.Y = 30, 30 // cell (0,0)
	p.Sel = 0
	r := s.TryBuild(0)
	if !r.Ok || r.What != "build" || r.Type != "arrow" {
		t.Fatalf("build failed: %+v", r)
	}
	if s.Gold != 90-36 || len(s.Grid) != 1 {
		t.Fatalf("gold=%d grid=%d", s.Gold, len(s.Grid))
	}
	// blocked: path tile
	p.X, p.Y = 30, 90 // cell (0,1) is path
	if r := s.TryBuild(0); r.Ok || r.Why != "blocked" {
		t.Fatalf("path build allowed: %+v", r)
	}
	// blocked: occupied
	p.X, p.Y = 30, 30
	if r := s.TryBuild(0); r.Ok || r.Why != "full" {
		t.Fatalf("double build allowed: %+v", r)
	}
	// repair: damage then repair at 75%
	s.Grid[0].HP = 40
	cost := int(math.Ceil(36 * 0.75))
	g0 := s.Gold
	if r := s.TryBuild(0); !r.Ok || r.What != "repair" {
		t.Fatalf("repair failed: %+v", r)
	}
	if s.Gold != g0-cost || s.Grid[0].HP != 100 {
		t.Fatalf("repair wrong: gold=%d hp=%v", s.Gold, s.Grid[0].HP)
	}
	// poor
	s.Gold = 0
	s.Grid[0].HP = 10
	if r := s.TryBuild(0); r.Ok || r.Why != "poor" {
		t.Fatalf("free repair: %+v", r)
	}
	// combat pricing +25%
	s.Phase = "combat"
	if s.TowerCost("arrow") != int(math.Ceil(36*1.25)) {
		t.Fatalf("combat price: %d", s.TowerCost("arrow"))
	}
	s.Phase = "build"
	if s.TowerCost("arrow") != 36 {
		t.Fatal("build price changed")
	}
}

func TestWaveComposition(t *testing.T) {
	s := testSim()
	s.Level, s.Wave = 1, 0
	q := s.BuildWave()
	if len(q) != 7+1*3+1*2 { // 7+wv*3+lv*2, wv=1
		t.Fatalf("wave size %d", len(q))
	}
	s.Level, s.Wave = 2, 2 // wv=3 -> lord appended
	q = s.BuildWave()
	lords := 0
	for _, k := range q {
		if k == "lord" {
			lords++
		}
	}
	if lords != 1 {
		t.Fatalf("lords=%d", lords)
	}
	s.Phase = "build"
	if !s.StartWave() {
		t.Fatal("start_wave refused in build")
	}
	if s.Phase != "combat" || s.Wave != 3 {
		t.Fatalf("wave not started: %s w%d", s.Phase, s.Wave)
	}
	s.Phase = "combat"
	if s.StartWave() {
		t.Fatal("start_wave allowed mid-combat")
	}
}

func TestTowersKillWalkers(t *testing.T) {
	s := testSim()
	now := 1000.0
	s.Phase = "combat"
	s.Level = 1
	// arrow tower near path start
	s.Grid = append(s.Grid, &Tower{C: 0, R: 0, Type: "arrow", HP: 100, X: 30, Y: 30})
	s.SpawnEnemy("walker")
	if len(s.Enemies) != 1 {
		t.Fatal("no spawn")
	}
	e := s.Enemies[0]
	if e.X != Waypts[0].X || e.SegLen != SegLens[0] {
		t.Fatal("spawn position wrong")
	}
	g0 := s.Gold
	for k := range 600 {
		now += 0.05
		s.Step(0.05, now)
		_ = k
		if e.Dead {
			break
		}
	}
	if !e.Dead {
		t.Fatal("arrow never killed the walker")
	}
	if s.Gold != g0+4+15+6*1 { // bounty 4 + wave-clear bonus 15+6*level
		t.Fatalf("gold wrong: %d", s.Gold-g0)
	}
}

func TestCannonSplashFrostSlow(t *testing.T) {
	s := testSim()
	now := 1000.0
	s.Phase = "combat"
	s.Level = 1
	s.Grid = append(s.Grid, &Tower{C: 5, R: 5, Type: "cannon", HP: 160, X: 330, Y: 330})
	a := &Enemy{Kind: "walker", X: 330, Y: 300, HP: 100, MaxHP: 100, Spd: 55, SegLen: 60}
	b := &Enemy{Kind: "walker", X: 360, Y: 300, HP: 100, MaxHP: 100, Spd: 55, SegLen: 60}
	s.Enemies = append(s.Enemies, a, b)
	now += 0.1
	s.Step(0.1, now)
	if a.HP >= 100 || b.HP >= 100 {
		t.Fatalf("no splash: %v %v", a.HP, b.HP)
	}
	// frost applies slowT
	s.Grid = []*Tower{{C: 5, R: 5, Type: "frost", HP: 120, X: 330, Y: 330}}
	c := &Enemy{Kind: "runner", X: 330, Y: 300, HP: 50, MaxHP: 50, Spd: 95, SegLen: 60}
	s.Enemies = append(s.Enemies, c)
	now += 0.1
	s.Step(0.1, now)
	if c.SlowT <= 0 {
		t.Fatal("frost did not slow")
	}
}

func TestLeakGameOver(t *testing.T) {
	s := testSim()
	now := 1000.0
	s.Phase = "combat"
	s.Level = 1
	s.Base = 2
	e := &Enemy{Kind: "brute", X: 0, Y: 0, HP: 100, MaxHP: 100, Spd: 10000,
		Seg: len(SegLens) - 1, SegT: SegLens[len(SegLens)-1] - 1, SegLen: SegLens[len(SegLens)-1]}
	s.Enemies = append(s.Enemies, e)
	now += 0.1
	s.Step(0.1, now)
	if s.Base != 0 || s.Phase != "over" || s.LastOver == nil {
		t.Fatalf("no game over: base=%d phase=%s", s.Base, s.Phase)
	}
}

func TestSmashDamagesTowers(t *testing.T) {
	s := testSim()
	now := 1000.0
	s.Phase = "combat"
	tw := &Tower{C: 5, R: 2, Type: "arrow", HP: 100, X: 330, Y: 130}
	s.Grid = append(s.Grid, tw)
	// brute ON path segment 5 ((330,90)->(390,90)); tower 40px below it
	e := &Enemy{Kind: "brute", X: 330, Y: 90, HP: 200, MaxHP: 200, Spd: 0,
		Seg: 5, SegT: 0, SegLen: SegLens[5]}
	s.Enemies = append(s.Enemies, e)
	now += 0.1
	s.Step(0.1, now)
	if tw.HP != 90 {
		t.Fatalf("no smash: hp=%v", tw.HP)
	}
}

func TestWaveClearLevelsUp(t *testing.T) {
	s := testSim()
	now := 1000.0
	s.Phase = "combat"
	s.Level, s.Wave = 1, 3 // wave >= level+2 -> next level on clear
	s.Gold = 0
	s.Grid = append(s.Grid, &Tower{C: 0, R: 0, Type: "arrow", HP: 50, X: 30, Y: 30})
	now += 0.1
	s.Step(0.1, now) // empty queue + no enemies -> clear
	if s.Level != 2 || s.Phase != "build" {
		t.Fatalf("no level-up: lvl=%d phase=%s", s.Level, s.Phase)
	}
	if s.Gold != 15+6*1 {
		t.Fatalf("clear bonus: %d", s.Gold)
	}
	if s.Grid[0].HP != 60 { // 50 + 10% of 100
		t.Fatalf("tower heal: %v", s.Grid[0].HP)
	}
}

func TestAvatarClampAndSel(t *testing.T) {
	s := testSim()
	p := s.Players[0]
	p.X, p.Y = 30, 30
	p.IX, p.IY = -1, -1
	s.Step(1.0, 1000.0)
	if p.X != 20 || p.Y != 20 {
		t.Fatalf("clamp failed: %v,%v", p.X, p.Y)
	}
}

func TestPauseShift(t *testing.T) {
	s := testSim()
	s.CountdownEnd, s.BuildEnd = 1100, 1110
	s.ShiftPaused(30)
	if s.CountdownEnd != 1130 || s.BuildEnd != 1140 {
		t.Fatal("shift missed a deadline")
	}
}
