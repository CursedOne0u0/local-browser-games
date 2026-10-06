// Sling-shot pad tests: velocity fling along the arrow with linear decay,
// no teleport, and a descending input-speed boost back to base speed.
package bomb

import (
	"math"
	"testing"
	"time"
)

func slingGame() *gameT {
	g := newGame()
	g.applyArena(2)
	g.phase = "playing"
	g.players[0] = &player{ID: "a"}
	g.holder = 2 // seat 0 runs at RUN_SPEED
	g.fuse = 30  // newGame leaves fuse at 0 (set by newRound); keep the bomb lit
	g.pos[0] = pos{X: 500, Y: 280} // free ice: (400,280) sits inside a pillar
	g.pads = []pad{{X: 500, Y: 280, DX: 1, DY: 0, Expires: time.Now().Add(time.Minute)}}
	return g
}

func TestSlingNoTeleport(t *testing.T) {
	g := slingGame()
	g.padTimer = 999 // no respawns mid-test
	g.step(0.016)
	if g.lastBoost == nil {
		t.Fatal("pad did not trigger")
	}
	if dx := g.pos[0].X - 500; dx >= 5 {
		t.Fatalf("trigger step teleported: dx=%v (old blink was 95)", dx)
	}
	g.step(0.016)
	dx := g.pos[0].X - 500
	if dx <= 5 || dx >= 45 {
		t.Fatalf("second-step displacement not sling-like: dx=%v", dx)
	}
	if math.Abs(g.pos[0].Y-280) > 1 {
		t.Fatalf("sling went off-arrow: y=%v", g.pos[0].Y)
	}
}

func TestSlingDecaysToZero(t *testing.T) {
	g := slingGame()
	g.padTimer = 999 // one sling only: no respawned pads re-triggering mid-flight
	var xs []float64
	// NOTE: sling/boost decay runs on wall time, so pace steps in real time.
	for range 35 {
		g.step(0.016)
		time.Sleep(16 * time.Millisecond)
		xs = append(xs, g.pos[0].X)
	}
	total := xs[len(xs)-1] - 500
	// sling 650px/s over 0.45s linear decay ≈ 146px; allow wall/deflection slack
	if total < 90 || total > 200 {
		t.Fatalf("sling travel wrong: %v", total)
	}
	// speed must decay: first half of travel >> second half
	mid := xs[9] - 500
	if mid < total*0.55 {
		t.Fatalf("no decay profile: 10-step=%v total=%v", mid, total)
	}
	// after expiry the runner stands still with no input
	x0 := g.pos[0].X
	for range 10 {
		g.step(0.016)
		time.Sleep(16 * time.Millisecond)
	}
	if math.Abs(g.pos[0].X-x0) > 1 {
		t.Fatalf("still moving after sling+boost expired: %v", g.pos[0].X-x0)
	}
}

func TestBoostMultDescends(t *testing.T) {
	g := slingGame()
	g.players[0].IX = 1 // run +x with the sling
	g.step(0.016)       // trigger step (sling starts next step)
	x0 := g.pos[0].X
	g.step(0.016)
	first := g.pos[0].X - x0
	// input part ≈ 235 * ~1.68 * 0.016 ≈ 6.3 plus sling ≈ 10 → well above base 3.8
	if first < 10 || first > 25 {
		t.Fatalf("boosted first step wrong: %v", first)
	}
}
