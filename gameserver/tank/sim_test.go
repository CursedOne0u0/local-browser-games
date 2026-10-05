// Deterministic sim tests: classic maze, fixed clock, no randomness.
package tank

import (
	"math"
	"testing"
)

func testSim() *Sim {
	s := New()
	s.ResetTanks(1000.0)
	s.Maze = classicMaze // AFTER reset: ResetTanks regenerates a random maze
	return s
}

func TestMazeInvariants(t *testing.T) {
	s := New()
	for trial := range 60 {
		m := s.genMaze()
		_ = trial
		for r := range Rows {
			if len(m[r]) != Cols {
				t.Fatalf("row %d len %d", r, len(m[r]))
			}
			for c := range Cols {
				mc, mr := Cols-1-c, Rows-1-r
				if m[r][c] != m[mr][mc] {
					t.Fatalf("not 180-symmetric at %d,%d", c, r)
				}
			}
		}
		// borders all walls
		for c := range Cols {
			if m[0][c] != '#' || m[Rows-1][c] != '#' {
				t.Fatal("top/bottom border breach")
			}
		}
		for r := range Rows {
			if m[r][0] != '#' || m[r][Cols-1] != '#' {
				t.Fatal("side border breach")
			}
		}
		// spawn cells open
		for _, cell := range spawnCells {
			if m[cell[1]][cell[0]] != '.' {
				t.Fatal("spawn blocked")
			}
		}
		// BFS spawn1 -> spawn2
		s1, s2 := spawnCells[0], spawnCells[1]
		seen := map[[2]int]bool{s1: true}
		stack := [][2]int{s1}
		for len(stack) > 0 {
			c, r := stack[len(stack)-1][0], stack[len(stack)-1][1]
			stack = stack[:len(stack)-1]
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				n := [2]int{c + d[0], r + d[1]}
				if 0 <= n[0] && n[0] < Cols && 0 <= n[1] && n[1] < Rows &&
					m[n[1]][n[0]] == '.' && !seen[n] {
					seen[n] = true
					stack = append(stack, n)
				}
			}
		}
		if !seen[s2] {
			t.Fatal("spawns disconnected")
		}
	}
}

func TestMovementAndTurn(t *testing.T) {
	s := testSim()
	s.Phase = "playing"
	t0 := s.Tanks[0]
	t0.X, t0.Y = 8.5*Cell, 5.5*Cell // open area in classic maze
	t0.Ang = 0
	s.Players[0] = &Player{ID: "a", Fwd: 1}
	x0 := t0.X
	s.Step(0.1, 1000.1)
	if t0.X <= x0 {
		t.Fatalf("tank did not advance: %v -> %v", x0, t0.X)
	}
	if math.Abs((t0.X-x0)-TankSpeed*0.1) > 1e-9 {
		t.Fatalf("speed wrong: dx=%v", t0.X-x0)
	}
	a0 := t0.Ang
	s.Players[0].Fwd, s.Players[0].Turn = 0, 1
	s.Step(0.1, 1000.2)
	if math.Abs((t0.Ang-a0)-TurnSpeed*0.1) > 1e-9 {
		t.Fatalf("turn wrong: da=%v", t0.Ang-a0)
	}
}

func TestFireKillRoundOver(t *testing.T) {
	s := testSim()
	s.Phase = "playing"
	a, b := s.Tanks[0], s.Tanks[1]
	a.X, a.Y, a.Ang = 8.5*Cell, 5.5*Cell, 0
	b.X, b.Y = 8.5*Cell+120, 5.5*Cell
	s.Players[0] = &Player{ID: "a", Fire: true}
	s.Step(1.0/60, 1000.0)
	if len(s.Bullets) != 1 {
		t.Fatalf("expected 1 bullet, got %d", len(s.Bullets))
	}
	if s.LastShot == nil || s.LastShot.By != 1 || s.LastShot.Kind != "shell" {
		t.Fatalf("bad last_shot: %+v", s.LastShot)
	}
	// let the bullet fly into tank 2
	now := 1000.0
	for range 120 {
		now += 1.0 / 60
		s.Step(1.0/60, now)
		if s.Phase != "playing" {
			break
		}
	}
	if b.Alive {
		t.Fatal("tank 2 should be dead")
	}
	if a.Score != 1 || s.Phase != "round" || s.RoundScored != 1 {
		t.Fatalf("bad round state: score=%d phase=%s", a.Score, s.Phase)
	}
	if s.LastKill == nil || s.LastKill.By != 1 || s.LastKill.Victim != 2 {
		t.Fatalf("bad last_kill: %+v", s.LastKill)
	}
	// round timer -> round 2, fresh maze positions
	now = s.RoundEnd + 0.1
	s.Step(0.01, now)
	if s.Phase != "playing" || s.Round != 2 || !a.Alive || !b.Alive {
		t.Fatalf("round 2 not reset: %s r%d", s.Phase, s.Round)
	}
	// score to WinRounds -> over
	a.Score = WinRounds - 1
	s.kill(1, 0, now)
	if s.Phase != "over" || s.Winner != 1 {
		t.Fatalf("no match win: %s w%d", s.Phase, s.Winner)
	}
}

func TestSuicideScoresFoe(t *testing.T) {
	s := testSim()
	s.kill(0, 0, 1000.0)
	if s.Tanks[1].Score != 1 || s.LastKill.Suicide != true {
		t.Fatalf("suicide mis-scored: %+v", s.LastKill)
	}
}

func TestShieldBlocksOnce(t *testing.T) {
	s := testSim()
	s.Phase = "playing"
	b := s.Tanks[1]
	b.X, b.Y = 8.5*Cell, 5.5*Cell
	b.Wpn, b.WpnUntil = "shield", 2000.0
	s.Bullets = append(s.Bullets, &Bullet{X: b.X - 10, Y: b.Y, VX: 100, VY: 0,
		Owner: 0, Born: 999.0, Grace: 999.0})
	s.Step(1.0/60, 1000.0)
	if !b.Alive || b.Wpn != "" || s.LastBlock == nil {
		t.Fatalf("shield did not absorb: alive=%v wpn=%q", b.Alive, b.Wpn)
	}
}

func TestMineLaysAndKills(t *testing.T) {
	s := testSim()
	s.Phase = "playing"
	a, b := s.Tanks[0], s.Tanks[1]
	a.X, a.Y, a.Ang = 8.5*Cell, 5.5*Cell, 0
	a.Wpn, a.WpnUntil = "mines", 2000.0
	s.fireBullet(0, 1000.0)
	if len(s.Mines) != 1 {
		t.Fatal("no mine laid")
	}
	if s.LastShot == nil || s.LastShot.Kind != "" {
		t.Fatalf("mine last_shot should have no kind: %+v", s.LastShot)
	}
	// unarmed mine must not trigger
	b.X, b.Y = s.Mines[0].X, s.Mines[0].Y
	s.Step(0.01, 1000.5)
	if !b.Alive {
		t.Fatal("unarmed mine killed")
	}
	// armed mine kills foe first
	s.Step(0.01, 1001.5)
	if b.Alive || a.Score != 1 {
		t.Fatal("armed mine did not kill foe")
	}
}

func TestRailChargeFiresBeam(t *testing.T) {
	s := testSim()
	s.Phase = "playing"
	a, b := s.Tanks[0], s.Tanks[1]
	a.X, a.Y, a.Ang = 1.5*Cell, 10.5*Cell, 0
	a.Wpn, a.WpnUntil = "rail", 2000.0
	b.X, b.Y = 5.5*Cell, 10.5*Cell // same corridor, before the col-8 wall
	s.fireBullet(0, 1000.0)
	if a.RailCharge != RailCharge {
		t.Fatal("no rail charge started")
	}
	if s.LastShot.Kind != "charge" {
		t.Fatalf("bad charge shot: %+v", s.LastShot)
	}
	now := 1000.0
	for range 70 {
		now += 1.0 / 60
		s.Step(1.0/60, now)
		if s.LastBeam != nil {
			break
		}
	}
	if s.LastBeam == nil {
		t.Fatal("rail never fired")
	}
	if b.Alive {
		t.Fatal("rail beam missed tank in open corridor")
	}
}

func TestSpreadRapidHoming(t *testing.T) {
	s := testSim()
	s.Phase = "playing"
	a := s.Tanks[0]
	a.X, a.Y, a.Ang = 8.5*Cell, 5.5*Cell, 0
	a.Wpn, a.WpnUntil = "spread", 2000.0
	s.fireBullet(0, 1000.0)
	if len(s.Bullets) != 3 {
		t.Fatalf("spread fired %d", len(s.Bullets))
	}
	s.Bullets = nil
	a.Wpn = "homing"
	a.CD = 0 // cooldown from the spread shot is still ticking
	s.fireBullet(0, 1001.0)
	if !s.Bullets[0].Home {
		t.Fatal("homing flag missing")
	}
}

func TestPauseShift(t *testing.T) {
	s := testSim()
	s.CountdownEnd = 1100.0
	s.Tanks[0].WpnUntil = 1200.0
	s.Bullets = append(s.Bullets, &Bullet{Born: 1000.0, Grace: 1000.4})
	s.Mines = append(s.Mines, &Mine{ArmedAt: 1001.0})
	s.Pickups = append(s.Pickups, &Pickup{Born: 990.0})
	s.ShiftPaused(30.0)
	if s.CountdownEnd != 1130.0 || s.Tanks[0].WpnUntil != 1230.0 ||
		s.Bullets[0].Born != 1030.0 || s.Bullets[0].Grace != 1030.4 ||
		s.Mines[0].ArmedAt != 1031.0 || s.Pickups[0].Born != 1020.0 {
		t.Fatal("shift missed a deadline")
	}
}

func TestPickupCollectExpire(t *testing.T) {
	s := testSim()
	s.Phase = "playing"
	a := s.Tanks[0]
	a.X, a.Y = 8.5*Cell, 5.5*Cell
	s.Pickups = append(s.Pickups, &Pickup{X: a.X, Y: a.Y, Kind: "rapid", Born: 1000.0})
	s.Step(0.01, 1000.5)
	if a.Wpn != "rapid" || s.LastPickup == nil || s.LastPickup.Tank != 1 {
		t.Fatalf("no collect: wpn=%q", a.Wpn)
	}
	if a.WpnUntil != 1000.5+weaponDur["rapid"] {
		t.Fatalf("bad wpn_until %v", a.WpnUntil)
	}
	// expiry
	s.Pickups = append(s.Pickups, &Pickup{X: 1.5 * Cell, Y: 1.5 * Cell, Kind: "rail", Born: 900.0})
	s.Step(0.01, 916.0)
	for _, p := range s.Pickups {
		if p.Kind == "rail" {
			t.Fatal("stale pickup not expired")
		}
	}
	// weapon times out
	s.Step(0.01, a.WpnUntil+1)
	if a.Wpn != "" {
		t.Fatal("weapon did not expire")
	}
}

func TestWallBlocksTank(t *testing.T) {
	s := testSim()
	s.Phase = "playing"
	a := s.Tanks[0]
	a.X, a.Y, a.Ang = 1.5*Cell, 1.5*Cell, math.Pi // facing left wall
	s.Players[0] = &Player{ID: "a", Fwd: 1}
	x0 := a.X
	for range 60 {
		s.Step(1.0/60, 1000.0)
	}
	if a.X < 70 {
		t.Fatalf("tank tunneled through border wall: %v -> %v", x0, a.X)
	}
}
