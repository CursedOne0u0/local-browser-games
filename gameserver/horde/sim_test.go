// Deterministic horde sim tests: fixed clock, direct state setup.
package horde

import (
	"math"
	"testing"
)

func testSim() *Sim {
	s := New()
	p := Mkbuild()
	p.ID, p.Name = "a", "A"
	p.LastSeen = 1000
	p.Ready = true
	s.Players[0] = p
	return s
}

func startPlaying(s *Sim, now float64) {
	s.EnterCountdown(now)
	s.Step(0.01, s.CountdownEnd+0.01)
	if s.Phase != "playing" {
		panic("no playing")
	}
	// clear the opening draft so the player can act
	s.Players[0].Pending = nil
}

func TestDraftOptionsShape(t *testing.T) {
	s := testSim()
	b := s.Players[0]
	opts := s.DraftOptions(b)
	if len(opts) != 3 {
		t.Fatalf("want 3 picks, got %d", len(opts))
	}
	seen := map[string]bool{}
	for _, o := range opts {
		if o.ID == "" || o.Name == "" || seen[o.ID] {
			t.Fatalf("bad pick: %+v", o)
		}
		seen[o.ID] = true
	}
	// weapon pity: at least one weapon/evo offered to a fresh build
	hasW := false
	for _, o := range opts {
		if len(o.ID) > 2 && (o.ID[:2] == "w_" || o.ID[:2] == "e_") {
			hasW = true
		}
	}
	if !hasW {
		t.Fatal("fresh build got no weapon offer")
	}
}

func TestApplyPickStats(t *testing.T) {
	s := testSim()
	b := s.Players[0]
	s.ApplyPick(0, "w_blades")
	if b.Wpn["w_blades"] != 1 {
		t.Fatal("weapon not added")
	}
	sp0 := b.Speed
	s.ApplyPick(0, "s_speed")
	if math.Abs(b.Speed-sp0*1.08) > 1e-9 {
		t.Fatal("speed scaling wrong")
	}
	s.ApplyPick(0, "s_cdr")
	if math.Abs(b.CDR-0.92) > 1e-9 {
		t.Fatal("cdr wrong")
	}
	for range 20 {
		s.ApplyPick(0, "s_cdr")
	}
	if b.CDR < 0.55 {
		t.Fatal("cdr floor breached")
	}
	s.ApplyPick(0, "a_life")
	if b.Lifesteal != 0.04 || len(b.Arti) != 1 {
		t.Fatal("artifact wrong")
	}
	hp0 := b.HP
	s.ApplyPick(0, "s_heal")
	if b.HP != math.Min(b.MaxHP, hp0+40) {
		t.Fatal("heal wrong")
	}
}

func TestXpLevelsQueueOne(t *testing.T) {
	s := testSim()
	now := 1000.0
	b := s.Players[0]
	s.GainXp(0, 1000, now)
	if b.Level <= 1 || b.Pending == nil {
		t.Fatalf("no level: lvl=%d", b.Level)
	}
	if s.LastLvl == nil || s.LastLvl.Seat != 1 {
		t.Fatal("no last_lvl event")
	}
	// further xp does not stack more pendings
	n := len(b.Pending)
	s.GainXp(0, 1000, now)
	if len(b.Pending) != n {
		t.Fatal("level queued twice")
	}
}

func TestSplitterSpawnsChasers(t *testing.T) {
	s := testSim()
	now := 1000.0
	e := &Enemy{X: 100, Y: 100, HP: 5, MaxHP: 50, Kind: "splitter", Tier: 1, Mode: "chase"}
	s.Enemies = append(s.Enemies, e)
	n0 := len(s.Enemies)
	s.HurtEnemy(e, 10, 0, now)
	if !e.Dead || s.Kills != 1 {
		t.Fatal("splitter did not die")
	}
	if len(s.Enemies) != n0+2 {
		t.Fatalf("no minions: %d", len(s.Enemies))
	}
	if s.Players[0].Kills != 1 {
		t.Fatal("owner kill not counted")
	}
	if len(s.Gems) != 1 {
		t.Fatalf("gems: %d", len(s.Gems))
	}
}

func TestReaperExecutes(t *testing.T) {
	s := testSim()
	now := 1000.0
	b := s.Players[0]
	b.Evo = append(b.Evo, "e_reaper")
	e := &Enemy{X: 0, Y: 0, HP: 14, MaxHP: 100, Kind: "chaser", Mode: "chase"}
	s.Enemies = append(s.Enemies, e)
	s.HurtEnemy(e, 1, 0, now) // 14% hp < 15% -> execute for full maxhp
	if !e.Dead {
		t.Fatal("reaper did not execute")
	}
}

func TestHurtPlayerDownAndProt(t *testing.T) {
	s := testSim()
	now := 1000.0
	b := s.Players[0]
	b.Prot = now + 10
	s.HurtPlayer(0, 50, now)
	if b.HP != 100 {
		t.Fatal("prot ignored")
	}
	b.Prot = 0
	b.Armor = 2
	s.HurtPlayer(0, 10, now)
	if b.HP != 92 {
		t.Fatalf("armor wrong: hp=%v", b.HP)
	}
	s.HurtPlayer(0, 1000, now)
	if !b.Down || b.HP != 0 || b.Bleed != 25 {
		t.Fatalf("down wrong: %+v", b)
	}
	// drafting invulnerability
	b.Down = false
	b.HP = 50
	b.Pending = []*DraftOpt{{ID: "s_hp"}}
	s.HurtPlayer(0, 40, now)
	if b.HP != 50 {
		t.Fatal("drafting player took damage")
	}
	b.Pending = nil
}

func TestReviveFlow(t *testing.T) {
	s := testSim()
	now := 1000.0
	p1 := Mkbuild()
	p1.ID, p1.Name = "b", "B"
	p1.Ready = true
	s.Players[1] = p1
	startPlaying(s, now)
	a, b := s.Players[0], s.Players[1]
	a.Down = true
	a.Bleed = 25
	a.X, a.Y = 0, 0
	b.X, b.Y = 10, 0 // within 60: reviving
	for k := range 100 {
		now += 0.1
		s.Step(0.1, now)
		_ = k
		if !a.Down {
			break
		}
	}
	if a.Down {
		t.Fatal("revive never completed")
	}
	if a.HP != a.MaxHP*0.5 {
		t.Fatalf("revive hp: %v", a.HP)
	}
	// bleed-out when alone
	a.Down = true
	a.Bleed = 0.3
	b.X, b.Y = 5000, 5000
	now += 1.0
	s.Step(0.5, now)
	if !a.Dead {
		t.Fatal("no bleed-out")
	}
}

func TestAllDownGameOver(t *testing.T) {
	s := testSim()
	now := 1000.0
	startPlaying(s, now)
	b := s.Players[0]
	b.Down = true
	b.Bleed = 25
	s.Step(0.05, now+0.1)
	if s.Phase != "over" || s.LastOver == nil {
		t.Fatalf("no game over: %s", s.Phase)
	}
}

func TestSpawningAndTiers(t *testing.T) {
	s := testSim()
	now := 1000.0
	startPlaying(s, now)
	b := s.Players[0]
	b.X, b.Y = 0, 0
	if TierAt(0, 0) != 0 || TierAt(5000, 0) != 1 || TierAt(20000, 0) != 2 {
		t.Fatal("tier rings wrong")
	}
	n0 := len(s.Enemies)
	for k := range 200 {
		now += 0.05
		s.Step(0.05, now)
		_ = k
		if len(s.Enemies) > n0 {
			break
		}
	}
	if len(s.Enemies) <= n0 {
		t.Fatal("nothing spawned in 10s")
	}
	for _, e := range s.Enemies {
		d := math.Hypot(e.X-b.X, e.Y-b.Y)
		if d < SpawnMin-1 || d > SpawnMax+600 {
			t.Fatalf("spawn ring violated: %v", d)
		}
	}
}

func TestGemMagnetCollect(t *testing.T) {
	s := testSim()
	now := 1000.0
	startPlaying(s, now)
	b := s.Players[0]
	b.X, b.Y = 0, 0
	xp0 := b.XP
	s.Gems = append(s.Gems, &Gem{X: 20, Y: 0, V: 2})
	for k := range 60 {
		now += 0.05
		s.Step(0.05, now)
		_ = k
		if len(s.Gems) == 0 {
			break
		}
	}
	if len(s.Gems) != 0 || b.XP <= xp0 {
		t.Fatalf("gem not collected: gems=%d xp=%d", len(s.Gems), b.XP)
	}
}

func TestChargerModes(t *testing.T) {
	s := testSim()
	now := 1000.0
	startPlaying(s, now)
	b := s.Players[0]
	b.X, b.Y = 0, 0
	e := &Enemy{X: 300, Y: 0, HP: 100, MaxHP: 100, Spd: 66, Kind: "charger",
		R: 15, Mode: "chase"}
	s.Enemies = append(s.Enemies, e)
	sawWind, sawDash := false, false
	for k := range 400 {
		now += 0.05
		s.Step(0.05, now)
		_ = k
		if e.Mode == "wind" {
			sawWind = true
		}
		if e.Mode == "dash" {
			sawDash = true
		}
		if sawWind && sawDash && e.Mode == "chase" {
			break
		}
	}
	if !sawWind || !sawDash {
		t.Fatalf("charger cycled poorly: wind=%v dash=%v mode=%s", sawWind, sawDash, e.Mode)
	}
}

func TestGoblinFleesAndLeaves(t *testing.T) {
	s := testSim()
	now := 1000.0
	startPlaying(s, now)
	b := s.Players[0]
	b.X, b.Y = 0, 0
	g := s.SpawnGoblin(now)
	g.X, g.Y = 200, 0
	s.Enemies = append(s.Enemies, g)
	x0 := g.X
	now += 0.5
	s.Step(0.5, now)
	if g.X <= x0 {
		t.Fatal("goblin did not flee")
	}
	now = g.Despawn + 1
	s.Step(0.05, now)
	if !g.Dead {
		t.Fatal("goblin did not despawn")
	}
}

func TestBladesKillAndGems(t *testing.T) {
	s := testSim()
	now := 1000.0
	startPlaying(s, now)
	b := s.Players[0]
	b.X, b.Y = 0, 0
	b.Wpn["w_blades"] = 1
	e := &Enemy{X: 100, Y: 0, HP: 10, MaxHP: 22, Spd: 96, Dmg: 8, R: 13,
		Kind: "chaser", Tier: 0, Mode: "chase"}
	s.Enemies = append(s.Enemies, e)
	k0 := s.Kills
	for k := range 300 {
		now += 0.05
		s.Step(0.05, now)
		_ = k
		if e.Dead {
			break
		}
	}
	if !e.Dead || s.Kills != k0+1 {
		t.Fatal("blades never killed the chaser")
	}
	if len(s.Gems) == 0 {
		t.Fatal("no gem dropped")
	}
}

func TestBoltsFireShots(t *testing.T) {
	s := testSim()
	now := 1000.0
	startPlaying(s, now)
	b := s.Players[0]
	b.X, b.Y = 0, 0
	b.Wpn["w_bolts"] = 2
	s.Enemies = append(s.Enemies, &Enemy{X: 300, Y: 0, HP: 100, MaxHP: 100,
		Spd: 96, R: 13, Kind: "chaser", Mode: "chase"})
	for k := range 60 {
		now += 0.05
		s.Step(0.05, now)
		_ = k
		if len(s.Shots) > 0 {
			break
		}
	}
	if len(s.Shots) == 0 {
		t.Fatal("bolts never fired")
	}
}

func TestPauseShift(t *testing.T) {
	s := testSim()
	s.CountdownEnd = 1100
	s.BloodUntil = 1200
	s.Players[0].Prot = 1150
	s.Players[0].Pending = []*DraftOpt{{ID: "x"}}
	s.Players[0].PendT = 1160
	s.Acids = []*Acid{{Until: 1170}}
	s.Fx = []*Fx{{Until: 1180}}
	s.ShiftPaused(30)
	b := s.Players[0]
	if s.CountdownEnd != 1130 || s.BloodUntil != 1230 || b.Prot != 1180 ||
		b.PendT != 1190 || s.Acids[0].Until != 1200 || s.Fx[0].Until != 1210 {
		t.Fatal("shift missed a deadline")
	}
}
