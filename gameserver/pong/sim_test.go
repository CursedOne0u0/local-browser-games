// Deterministic pong sim tests: fixed clock, seeded rng where needed.
package pong

import (
	"math"
	"testing"
)

func testSim() *Sim {
	s := New()
	s.Players[0] = &Player{ID: "a", Name: "A", Y: 0.5, PrevY: 0.5}
	s.Players[1] = &Player{ID: "b", Name: "B", Y: 0.5, PrevY: 0.5}
	return s
}

func startPlaying(s *Sim, now float64) {
	s.EnterCountdown(now)
	s.Step(0.01, now+0.01) // waiting -> countdown? no: EnterCountdown sets countdown already
	_ = now
}

func TestCountdownServePreviewRelease(t *testing.T) {
	s := testSim()
	if !s.ShouldStart() == false {
		_ = s
	}
	s.Players[0].Ready = true
	s.Players[1].Ready = true
	if !s.ShouldStart() {
		t.Fatal("should start when both ready")
	}
	now := 1000.0
	s.EnterCountdown(now)
	if s.Phase != "countdown" {
		t.Fatal("no countdown")
	}
	s.Step(0.1, now+2.5) // countdown ends -> playing + serve (armed, held)
	if s.Phase != "playing" || !s.ServeArmed {
		t.Fatalf("serve not armed: %s %v", s.Phase, s.ServeArmed)
	}
	if s.Ball.VX != 0 || s.Ball.VY != 0 {
		t.Fatal("ball should be held during preview")
	}
	vx, vy := s.ServeVX, s.ServeVY
	if vx == 0 && vy == 0 {
		t.Fatal("no serve velocity stored")
	}
	s.Step(0.1, s.ServePreviewUntil+0.01) // preview over -> release
	if s.ServeArmed || s.Ball.VX != vx || s.Ball.VY != vy {
		t.Fatal("serve not released with stored velocity")
	}
}

func TestPaddleBounceScoresRally(t *testing.T) {
	s := testSim()
	now := 1000.0
	s.Phase = "playing"
	s.ServeArmed = false
	b := s.Ball
	b.X, b.Y = 50, s.P1.Y*H
	b.VX, b.VY = -400, 0
	s.Step(1.0/60, now)
	if b.VX <= 0 || b.LastHit != 1 || s.Rally != 1 {
		t.Fatalf("no p1 bounce: vx=%v hit=%d rally=%d", b.VX, b.LastHit, s.Rally)
	}
	if b.Speed <= 400 {
		t.Fatalf("bounce should speed up: %v", b.Speed)
	}
}

func TestPointAndWinBy2(t *testing.T) {
	s := testSim()
	now := 1000.0
	s.Point(1, now)
	if s.P1.Score != 1 || s.Phase != "point" || s.PointScored != 1 {
		t.Fatalf("bad point: %+v", s.LastPoint)
	}
	if s.LastPoint == nil || s.LastPoint.Scored != 1 {
		t.Fatal("no last_point event")
	}
	// point_end -> playing, serve toward... point_scored==1 -> serve_dir -1
	s.Step(0.01, s.PointEnd+0.01)
	if s.Phase != "playing" || s.ServeDir != 1 {
		// serve() flips: set -1 then serve flips to +1
		t.Fatalf("bad re-serve: phase=%s dir=%d", s.Phase, s.ServeDir)
	}
	// win by 2: 7-6 is not over (win=7)
	s.P1.Score, s.P2.Score = 6, 6
	s.Point(1, now)
	if s.Phase != "point" {
		t.Fatal("7-6 should not end the match (win by 2)")
	}
	if !s.Sudden {
		t.Fatal("6-6 should trigger sudden death")
	}
	s.Point(1, now)
	if s.Phase != "over" || s.Winner != 1 {
		t.Fatalf("8-6 should end it: %s w%d", s.Phase, s.Winner)
	}
}

func TestShieldBlocksGoal(t *testing.T) {
	s := testSim()
	now := 1000.0
	s.Players[0].Shield = true
	s.Point(2, now) // goal on p1 side, p1 shielded
	if s.P1.Score != 0 || s.P2.Score != 0 || s.Phase != "point" || s.PointScored != 0 {
		t.Fatal("shield did not void the goal")
	}
	if s.LastBlock == nil || s.LastBlock.By != 1 {
		t.Fatalf("bad last_block: %+v", s.LastBlock)
	}
	if s.Players[0].Shield {
		t.Fatal("shield not consumed")
	}
}

func TestPowerups(t *testing.T) {
	s := testSim()
	now := 1000.0
	s.ApplyPowerup("expand", 1, now)
	if s.P1.H != 140 {
		t.Fatal("expand failed")
	}
	s.ApplyPowerup("shrink", 1, now)
	if s.P2.H != 52 {
		t.Fatal("shrink hits the foe")
	}
	s.ApplyPowerup("shield", 2, now)
	if !s.Players[1].Shield {
		t.Fatal("shield not granted")
	}
	s.ApplyPowerup("freeze", 1, now)
	if s.Players[1].FrozenUntil != now+2.5 {
		t.Fatal("freeze failed")
	}
	s.ApplyPowerup("magnet", 2, now)
	if s.Players[1].MagnetUntil != now+8 {
		t.Fatal("magnet failed")
	}
	s.ApplyPowerup("ghost", 2, now)
	if s.GhostHiddenFor != 1 || s.GhostUntil != now+2.5 {
		t.Fatal("ghost failed")
	}
	y1, y2 := s.P1.Y, s.P2.Y
	s.Players[0].Y, s.Players[1].Y = 0.2, 0.8
	s.P1.Y, s.P2.Y = 0.2, 0.8
	s.ApplyPowerup("swap", 1, now)
	if s.P1.Y != 0.8 || s.P2.Y != 0.2 || s.Players[0].Y != 0.8 {
		t.Fatal("swap failed")
	}
	_, _ = y1, y2
	s.Ball.X, s.Ball.Y, s.Ball.VX, s.Ball.VY = 400, 250, 100, 0
	s.ApplyPowerup("vortex", 1, now)
	if s.Vortex == nil || s.Vortex.X != 400 || s.Vortex.Y != 250 {
		t.Fatalf("vortex not placed: %+v", s.Vortex)
	}
	if s.LastPower == nil || s.LastPower.Kind != "vortex" || s.LastPower.By != 1 {
		t.Fatalf("bad last_power: %+v", s.LastPower)
	}
	sp0 := math.Hypot(s.Ball.VX, s.Ball.VY)
	s.ApplyPowerup("turbo", 1, now)
	if math.Hypot(s.Ball.VX, s.Ball.VY) <= sp0 {
		t.Fatal("turbo did not accelerate")
	}
}

func TestObstacleLifecycle(t *testing.T) {
	s := testSim()
	now := 1000.0
	s.ObNext = now - 1
	s.ObstacleUpdate(now)
	if s.Obstacle == nil || s.Obstacle.Phase != "warn_in" {
		t.Fatal("obstacle did not spawn")
	}
	ob := s.Obstacle
	if ob.X < W/2-70 || ob.X > W/2+70 || ob.Len < 110 || ob.Len > 180 {
		t.Fatalf("bad spawn box: %+v", ob)
	}
	s.ObstacleUpdate(ob.Until + 0.01)
	if s.Obstacle.Phase != "active" {
		t.Fatal("warn_in -> active failed")
	}
	s.ObstacleUpdate(s.Obstacle.Until + 0.01)
	if s.Obstacle.Phase != "warn_out" {
		t.Fatal("active -> warn_out failed")
	}
	s.ObstacleUpdate(s.Obstacle.Until + 0.01)
	if s.Obstacle != nil || s.ObNext <= now {
		t.Fatal("warn_out did not clear + reschedule")
	}
	// disabled setting kills it
	s.Settings.Obstacle = false
	s.Obstacle = &Obstacle{Phase: "active"}
	s.ObstacleUpdate(now)
	if s.Obstacle != nil {
		t.Fatal("disabled obstacle not cleared")
	}
	s.Settings.Obstacle = true
}

func TestPauseShift(t *testing.T) {
	s := testSim()
	s.CountdownEnd, s.PointEnd = 1100, 1105
	s.ServePreviewUntil, s.ObNext, s.GhostUntil = 1110, 1120, 1130
	s.Obstacle = &Obstacle{Until: 1140}
	s.Vortex = &Vortex{Until: 1150}
	s.Powerup = &Powerup{Born: 900}
	s.Players[0].FrozenUntil, s.Players[0].OdUntil = 1160, 1170
	s.ShiftPaused(30)
	if s.CountdownEnd != 1130 || s.PointEnd != 1135 || s.ServePreviewUntil != 1140 ||
		s.ObNext != 1150 || s.GhostUntil != 1160 || s.Obstacle.Until != 1170 ||
		s.Vortex.Until != 1180 || s.Powerup.Born != 930 ||
		s.Players[0].FrozenUntil != 1190 || s.Players[0].OdUntil != 1200 {
		t.Fatal("shift missed a deadline")
	}
	// paddle effect_until is NOT shifted (mirrors Python quirk)
	s.P1.EffectUntil = 1000
	s.ShiftPaused(30)
	if s.P1.EffectUntil != 1000 {
		t.Fatal("effect_until must stay put")
	}
}

func TestBotDrivesP2(t *testing.T) {
	s := testSim()
	now := 1000.0
	s.Bot = true
	s.Phase = "playing"
	s.ServeArmed = false
	s.Ball.X, s.Ball.Y = 700, 400
	s.Ball.VX, s.Ball.VY = 300, 0
	y0 := s.P2.Y
	moved := false
	for range 60 {
		now += 1.0 / 60
		s.Step(1.0/60, now)
		if s.P2.Y != y0 {
			moved = true
		}
	}
	if !moved {
		t.Fatal("bot never moved P2")
	}
}
