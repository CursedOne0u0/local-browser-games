// The 8 auto-weapons for the horde sim.
package horde

import "math"

func (s *Sim) weapons(dt, now float64, occ []int) {
	for _, i := range occ {
		b := s.Players[i]
		if b.Down || b.Pending != nil {
			continue
		}
		if lv := b.Wpn["w_blades"]; lv > 0 {
			rate := 2.6 + 0.3*float64(lv)
			a0 := b.BladeA
			b.BladeA += dt * rate
			n := lv + 1
			rr := 95 + 10*float64(lv)
			for k := range n {
				pa := a0 + float64(k)*2*math.Pi/float64(n)
				ca := b.BladeA + float64(k)*2*math.Pi/float64(n)
				x1, y1 := b.X+math.Cos(pa)*rr, b.Y+math.Sin(pa)*rr
				x2, y2 := b.X+math.Cos(ca)*rr, b.Y+math.Sin(ca)*rr
				for _, e := range s.Enemies {
					if e.Dead || now < e.BladeHit {
						continue
					}
					if SegCircle(x1, y1, x2, y2, e.X, e.Y, e.R+10) {
						e.BladeHit = now + 0.35
						s.HurtEnemy(e, float64(8+4*lv)*b.Dmg, i, now)
					}
				}
			}
		}
		if lv := b.Wpn["w_bolts"]; lv > 0 {
			b.BoltT -= dt
			if b.BoltT <= 0 && len(s.Enemies) > 0 {
				b.BoltT = 1.1 * b.CDR
				var tgt *Enemy
				bd := math.MaxFloat64
				for _, e := range s.Enemies {
					if e.Dead {
						continue
					}
					if d := math.Hypot(e.X-b.X, e.Y-b.Y); d < bd {
						tgt, bd = e, d
					}
				}
				if tgt != nil {
					home := hasStr(b.Evo, "e_oracle")
					for k := range lv {
						a := math.Atan2(tgt.Y-b.Y, tgt.X-b.X) + (float64(k)-float64(lv-1)/2)*0.12
						s.Shots = append(s.Shots, &Shot{X: b.X, Y: b.Y,
							VX: math.Cos(a) * 700, VY: math.Sin(a) * 700,
							Dmg: float64(12+6*lv) * b.Dmg, Owner: i, Life: 1.6,
							Home: home})
					}
				}
			}
		}
		if lv := b.Wpn["w_nova"]; lv > 0 {
			b.NovaT -= dt
			if b.NovaT <= 0 {
				b.NovaT = (4.5 - 0.3*float64(lv)) * b.CDR
				thorn := hasStr(b.Evo, "e_thornova")
				rr := float64(130+15*lv)
				dmg := float64(20+10*lv) * b.Dmg
				if thorn {
					rr *= 1.8
					dmg *= 1.8
				}
				kind := "nova"
				if thorn {
					kind = "thornova"
				}
				s.EmitFx(kind, []any{[]any{r1(b.X), r1(b.Y), r1(rr)}}, now)
				for _, e := range s.Enemies {
					if e.Dead {
						continue
					}
					d := math.Hypot(e.X-b.X, e.Y-b.Y)
					if d < rr+e.R {
						s.HurtEnemy(e, dmg, i, now)
						if thorn {
							e.SlowUntil = now + 2.0
						}
						if d > 1 {
							e.KX += (e.X-b.X)/d*520
							e.KY += (e.Y-b.Y)/d*520
						}
					}
				}
			}
		}
		if lv := b.Wpn["w_frost"]; lv > 0 {
			b.FrostA += dt * 1.7
			n := 1 + lv/2
			for k := range n {
				a := b.FrostA + float64(k)*2*math.Pi/float64(n)
				x1, y1 := b.X+math.Cos(a)*120, b.Y+math.Sin(a)*120
				for _, e := range s.Enemies {
					if e.Dead || now < e.FrostHit {
						continue
					}
					if math.Hypot(x1-e.X, y1-e.Y) < e.R+10 {
						e.FrostHit = now + 0.4
						e.SlowUntil = now + 1.5
						s.HurtEnemy(e, float64(6+3*lv)*b.Dmg, i, now)
					}
				}
			}
		}
		if lv := b.Wpn["w_chain"]; lv > 0 {
			b.ChainT -= dt
			if b.ChainT <= 0 {
				if tgt := s.NearestEnemy(b.X, b.Y, 520, nil); tgt != nil {
					b.ChainT = (2.4 - 0.2*float64(lv)) * b.CDR
					pts := []any{[]any{r1(b.X), r1(b.Y)}}
					dmg := float64(16+9*lv) * b.Dmg
					seen := map[*Enemy]bool{}
					cur := tgt
					for range 2 + lv {
						if cur == nil || seen[cur] {
							break
						}
						seen[cur] = true
						pts = append(pts, []any{r1(cur.X), r1(cur.Y)})
						s.HurtEnemy(cur, dmg, i, now)
						dmg *= 0.78
						var nxt *Enemy
						bd := 230.0
						for _, e := range s.Enemies {
							if e.Dead || seen[e] {
								continue
							}
							if d := math.Hypot(e.X-cur.X, e.Y-cur.Y); d < bd {
								nxt, bd = e, d
							}
						}
						cur = nxt
					}
					s.EmitFx("chain", pts, now)
				}
			}
		}
		if lv := b.Wpn["w_msl"]; lv > 0 {
			b.MslT -= dt
			if b.MslT <= 0 && len(s.Enemies) > 0 {
				b.MslT = (2.8 - 0.25*float64(lv)) * b.CDR
				for range min(lv, 5) {
					a := s.rng.Float64() * 2 * math.Pi
					s.Shots = append(s.Shots, &Shot{X: b.X, Y: b.Y,
						VX: math.Cos(a) * 380, VY: math.Sin(a) * 380,
						Dmg: float64(14+8*lv) * b.Dmg, Owner: i, Life: 2.5,
						Home: true, Aoe: 46})
				}
			}
		}
		if lv := b.Wpn["w_axe"]; lv > 0 {
			b.AxeT -= dt
			if b.AxeT <= 0 {
				if tgt := s.NearestEnemy(b.X, b.Y, 640, nil); tgt != nil {
					b.AxeT = (3.0 - 0.25*float64(lv)) * b.CDR
					a := math.Atan2(tgt.Y-b.Y, tgt.X-b.X)
					s.ThrowN++
					s.Axes = append(s.Axes, &Axe{X: b.X, Y: b.Y,
						VX: math.Cos(a) * 520, VY: math.Sin(a) * 520,
						Owner: i, Dmg: float64(22+10*lv) * b.Dmg,
						MaxD: 340, Throw: s.ThrowN})
				}
			}
		}
		if lv := b.Wpn["w_acid"]; lv > 0 {
			b.AcidT -= dt
			if b.AcidT <= 0 {
				var bestc *Enemy
				bestn := 1
				for _, e := range s.Enemies {
					if e.Dead {
						continue
					}
					if math.Hypot(e.X-b.X, e.Y-b.Y) > 600 {
						continue
					}
					n := 0
					for _, o := range s.Enemies {
						if !o.Dead && math.Hypot(o.X-e.X, o.Y-e.Y) < 110 {
							n++
						}
					}
					if n > bestn {
						bestc, bestn = e, n
					}
				}
				if bestc != nil {
					b.AcidT = (5.0 - 0.4*float64(lv)) * b.CDR
					s.Acids = append(s.Acids, &Acid{X: bestc.X, Y: bestc.Y,
						R: 70 + 8*float64(lv), DPS: float64(10+6*lv) * b.Dmg,
						Owner: i, Until: now + 4.0})
				}
			}
		}
	}
}

func (s *Sim) stepShots(dt, now float64) {
	for _, sh := range s.Shots {
		ox, oy := sh.X, sh.Y
		if sh.Home {
			sp := math.Hypot(sh.VX, sh.VY)
			if sp == 0 {
				sp = 1
			}
			if tgt := s.NearestEnemy(sh.X, sh.Y, 700, nil); tgt != nil {
				cur := math.Atan2(sh.VY, sh.VX)
				want := math.Atan2(tgt.Y-sh.Y, tgt.X-sh.X)
				dd := math.Mod(want-cur+math.Pi, 2*math.Pi) - math.Pi
				na := cur + math.Max(-3.2*dt, math.Min(3.2*dt, dd))
				sh.VX, sh.VY = math.Cos(na)*sp, math.Sin(na)*sp
			}
		}
		sh.X += sh.VX * dt
		sh.Y += sh.VY * dt
		sh.Life -= dt
		if sh.Life <= 0 {
			sh.Dead = true
			continue
		}
		for _, e := range s.Enemies {
			if e.Dead {
				continue
			}
			if SegCircle(ox, oy, sh.X, sh.Y, e.X, e.Y, e.R+6) {
				sh.Dead = true
				s.HurtEnemy(e, sh.Dmg, sh.Owner, now)
				if sh.Aoe != 0 {
					for _, o := range s.Enemies {
						if o.Dead || o == e {
							continue
						}
						if math.Hypot(o.X-e.X, o.Y-e.Y) < sh.Aoe {
							s.HurtEnemy(o, sh.Dmg*0.5, sh.Owner, now)
						}
					}
					s.EmitFx("pop", []any{[]any{r1(e.X), r1(e.Y)}}, now)
				}
				break
			}
		}
	}
	kept := s.Shots[:0]
	for _, sh := range s.Shots {
		if !sh.Dead {
			kept = append(kept, sh)
		}
	}
	s.Shots = kept
}

func (s *Sim) stepAxes(dt, now float64) {
	_ = now
	for _, ax := range s.Axes {
		ox, oy := ax.X, ax.Y
		if !ax.Back {
			ax.X += ax.VX * dt
			ax.Y += ax.VY * dt
			ax.Dist += 520 * dt
			if ax.Dist >= ax.MaxD {
				ax.Back = true
			}
		} else {
			o := s.Players[ax.Owner]
			d := math.Hypot(o.X-ax.X, o.Y-ax.Y)
			if d == 0 {
				d = 1
			}
			ax.VX, ax.VY = (o.X-ax.X)/d*560, (o.Y-ax.Y)/d*560
			ax.X += ax.VX * dt
			ax.Y += ax.VY * dt
			if d < 34 {
				ax.Dead = true
				continue
			}
		}
		for _, e := range s.Enemies {
			if e.Dead || e.AxeHit == ax.Throw {
				continue
			}
			if SegCircle(ox, oy, ax.X, ax.Y, e.X, e.Y, e.R+12) {
				e.AxeHit = ax.Throw
				s.HurtEnemy(e, ax.Dmg, ax.Owner, now)
			}
		}
	}
	kept := s.Axes[:0]
	for _, ax := range s.Axes {
		if !ax.Dead {
			kept = append(kept, ax)
		}
	}
	s.Axes = kept
}

func r1(v float64) float64 { return math.Round(v*10) / 10 }
