// Deterministic echo sim tests: fixed clock, direct state setup.
package echo

import (
	"math"
	"testing"
)

func testSim() *Sim {
	s := New()
	for i := range 4 {
		s.Players[i] = &Player{ID: string(rune('a' + i)), Name: "P",
			LastSeen: 1000, Ready: true}
	}
	return s
}

func startMatch(s *Sim, now float64) {
	s.EnterCountdown(now)
	s.Step(0.01, s.CountdownEnd+0.01) // countdown -> playing
	if s.Phase != "playing" {
		panic("no playing")
	}
}

func TestRolesAndTarget(t *testing.T) {
	s := testSim()
	now := 1000.0
	s.AssignRoles(now)
	nh := len(s.Hunters())
	nd := len(s.Divers())
	if nh < 1 || nh+nd != 4 {
		t.Fatalf("bad roles: %d hunters %d divers", nh, nd)
	}
	if s.Target != 4+nd {
		t.Fatalf("bad target %d for %d divers", s.Target, nd)
	}
	if len(s.Nodes) != LiveNodes {
		t.Fatalf("expected %d nodes, got %d", LiveNodes, len(s.Nodes))
	}
	// nodes clear of hunters and spread
	for _, nd := range s.Nodes {
		for _, h := range s.Hunters() {
			if math.Hypot(nd.X-s.Runners[h].X, nd.Y-s.Runners[h].Y) < SpawnClearHunter {
				t.Fatal("node too close to hunter spawn")
			}
		}
		if !PointClear(nd.X, nd.Y, 30) {
			t.Fatal("node inside pillar")
		}
	}
}

func TestCollide(t *testing.T) {
	x, y := Collide(340, 290) // inside pillar 0 (300,250,80,80)
	if x == 340 && y == 290 {
		t.Fatal("no collision pushed out")
	}
	// pushed just outside the rect
	if x > 300-RunR-1 && x < 300+80+RunR+1 && y > 250-RunR-1 && y < 250+80+RunR+1 {
		// still intersecting ring radius? check distance to rect
		cx := math.Max(300, math.Min(380, x))
		cy := math.Max(250, math.Min(330, y))
		if math.Hypot(x-cx, y-cy) < RunR-0.01 {
			t.Fatalf("still inside pillar: %v,%v", x, y)
		}
	}
	x, y = Collide(-100, -100)
	if x != RunR || y != RunR {
		t.Fatalf("arena clamp failed: %v,%v", x, y)
	}
}

func TestChannelClaimsSlot(t *testing.T) {
	s := testSim()
	now := 1000.0
	startMatch(s, now)
	// find a diver and park them on a node
	var d int = -1
	for _, i := range s.Divers() {
		d = i
		break
	}
	nd := s.Nodes[0]
	s.Runners[d].X, s.Runners[d].Y = nd.X, nd.Y
	s.Players[d].IX, s.Players[d].IY = 0, 0
	now += 0.1
	s.Step(0.05, now) // channel starts
	if s.Channel[d] == nil {
		t.Fatal("channel did not start")
	}
	for now += 0.1; now < 1010; now += 0.1 {
		s.Step(0.05, now)
		if s.HoldsSlot(d) {
			break
		}
	}
	if !s.HoldsSlot(d) {
		t.Fatal("channel never claimed a slot")
	}
	if s.LastShout == nil {
		t.Fatal("claim should shout")
	}
}

func TestSolveCorrectAndWrong(t *testing.T) {
	s := testSim()
	now := 1000.0
	startMatch(s, now)
	var d int = -1
	for _, i := range s.Divers() {
		d = i
		break
	}
	nd := s.Nodes[0]
	sl := nd.Slots[0]
	sl.Solver = d
	var links [][2]int
	for dst, src := range sl.Perm {
		links = append(links, [2]int{src, dst})
	}
	out := s.SolveAttempt(d, links, true, now)
	if out["ok"] != true || !sl.Resolved || nd.Done != 1 {
		t.Fatalf("correct solve failed: %v", out)
	}
	sl2 := nd.Slots[1]
	sl2.Solver = d
	out = s.SolveAttempt(d, [][2]int{{0, 0}}, true, now)
	if out["ok"] != false {
		t.Fatal("wrong solve accepted")
	}
	out = s.SolveAttempt(d, nil, false, now)
	if out["ok"] != false {
		t.Fatal("bad links accepted")
	}
}

func TestNodeCrackRespawns(t *testing.T) {
	s := testSim()
	now := 1000.0
	startMatch(s, now)
	var d int = -1
	for _, i := range s.Divers() {
		d = i
		break
	}
	n0 := len(s.Nodes)
	nd := s.Nodes[0]
	for _, sl := range nd.Slots {
		sl.Solver = d
		var links [][2]int
		for dst, src := range sl.Perm {
			links = append(links, [2]int{src, dst})
		}
		if s.SolveAttempt(d, links, true, now)["ok"] != true {
			t.Fatal("solve failed")
		}
	}
	if s.Cracked != 1 {
		t.Fatalf("cracked=%d", s.Cracked)
	}
	if s.LastCollect == nil || s.LastCollect.Done != 1 || s.LastCollect.Target != s.Target {
		t.Fatalf("bad last_collect: %+v", s.LastCollect)
	}
	if len(s.Nodes) != n0 { // popped + respawned
		t.Fatalf("node count %d, want %d", len(s.Nodes), n0)
	}
}

func TestTagWipesDivers(t *testing.T) {
	s := testSim()
	now := 1000.0
	startMatch(s, now)
	h := s.Hunters()[0]
	for _, d := range s.Divers() {
		s.Runners[d].X, s.Runners[d].Y = s.Runners[h].X, s.Runners[h].Y
	}
	s.Step(0.05, now+0.1)
	for _, d := range s.Divers() {
		if s.Runners[d].Alive {
			t.Fatal("diver not tagged at zero distance")
		}
	}
	if s.LastTag == nil {
		t.Fatal("no last_tag")
	}
	if s.HWins != 1 || s.Phase != "round" {
		t.Fatalf("hunters should win the round: hwins=%d phase=%s", s.HWins, s.Phase)
	}
	// round break -> next round
	s.Step(0.05, s.RoundEnd+0.1)
	if s.Phase != "playing" || s.Round != 2 {
		t.Fatalf("no round 2: %s r%d", s.Phase, s.Round)
	}
}

func TestWinMatchAndRestart(t *testing.T) {
	s := testSim()
	now := 1000.0
	s.HWins = WinRounds - 1
	s.WinRound(1, "test", now)
	if s.Phase != "over" || s.WinnerSide != 1 {
		t.Fatalf("no match win: %s side=%d", s.Phase, s.WinnerSide)
	}
}

func TestStaminaGassed(t *testing.T) {
	s := testSim()
	now := 1000.0
	startMatch(s, now)
	var d int = -1
	for _, i := range s.Divers() {
		d = i
		break
	}
	s.Players[d].IX, s.Players[d].IY = 1, 0
	for k := range 200 {
		now += 0.05
		s.Step(0.05, now)
		_ = k
	}
	r := s.Runners[d]
	if !r.Gassed || r.Stam != 0 {
		t.Fatalf("diver should be gassed: stam=%v gassed=%v", r.Stam, r.Gassed)
	}
	if s.EffMag[d] != 0.3 {
		t.Fatalf("gassed cap not applied: %v", s.EffMag[d])
	}
	// rest recovers
	s.Players[d].IX, s.Players[d].IY = 0, 0
	for range 200 {
		now += 0.05
		s.Step(0.05, now)
	}
	if r.Stam < 0.3 || r.Gassed {
		t.Fatalf("no recovery: stam=%v gassed=%v", r.Stam, r.Gassed)
	}
}

func TestPauseShiftRingsAge(t *testing.T) {
	s := testSim()
	s.NextPingAt = 1100
	s.Blips = []Blip{{X: 1, Y: 2, Str: 1, Until: 1150}}
	s.Rings = []Ring{{X: 3, Y: 4, Born: 1000}}
	s.ShiftPaused(30)
	if s.NextPingAt != 1130 || s.Blips[0].Until != 1180 {
		t.Fatal("shift missed ping/blip")
	}
	if s.Rings[0].Born != 1000 {
		t.Fatal("rings must age through pause (Python quirk)")
	}
}

func TestSetBots(t *testing.T) {
	s := New()
	s.Players[0] = &Player{ID: "h", Name: "H", Ready: true}
	if n := s.SetBots(2, 1000.0); n != 2 {
		t.Fatalf("want 2 bots, got %d", n)
	}
	if !s.Players[1].Bot || !s.Players[2].Bot {
		t.Fatal("bots not in lowest free seats")
	}
	if s.Players[1].Name != "🤖 Byte" {
		t.Fatalf("bot name: %q", s.Players[1].Name)
	}
	if n := s.SetBots(0, 1000.0); n != 0 {
		t.Fatalf("bots not dropped: %d", n)
	}
	if s.Players[1] != nil {
		t.Fatal("bot seat not freed")
	}
}
