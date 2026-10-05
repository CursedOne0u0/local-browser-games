// Step function and match lifecycle for the horde sim.
package horde

import "math"

func (s *Sim) NearAny(x, y, r float64) bool {
	for _, i := range s.Occupied() {
		if math.Hypot(x-s.Players[i].X, y-s.Players[i].Y) < r {
			return true
		}
	}
	return false
}

// ShouldStart mirrors the auto-start condition in game_loop.
func (s *Sim) ShouldStart() bool {
	occ := s.Occupied()
	if s.Phase != "waiting" || len(occ) == 0 {
		return false
	}
	for _, i := range occ {
		if !s.Players[i].Ready {
			return false
		}
	}
	return true
}

// EnterCountdown performs the countdown init from game_loop.
func (s *Sim) EnterCountdown(now float64) {
	s.Phase = "countdown"
	s.CountdownEnd = now + 2.4
	s.Time = 0
	s.Enemies = nil
	s.Shots = nil
	s.Gems = nil
	s.Boss = nil
	s.Kills = 0
	s.Axes = nil
	s.Acids = nil
	s.Fx = nil
	s.SpawnT = 1.0
	s.EliteT = 30.0
	s.BossT = BossEvery
	s.BossN = 0
	s.BloodUntil = 0
	s.BloodNext = 180.0
	for _, i := range s.Occupied() {
		old := s.Players[i]
		nb := Mkbuild()
		nb.ID, nb.Name = old.ID, old.Name
		nb.LastSeen, nb.Ready = old.LastSeen, old.Ready
		nb.IX, nb.IY = 0, 0
		nb.X, nb.Y = float64(i%2)*400-200, float64(i/2)*400-200
		nb.Prot = now + 3
		s.Players[i] = nb
		b := nb
		b.Pending = s.DraftOptions(b)
		b.PendT = now + 30
		s.EventID++
		s.LastLvl = &LvlEv{Seat: i + 1, Lvl: 1, ID: s.EventID}
	}
}

// ShiftPaused thaws absolute deadlines (dt-driven timers freeze on their own).
func (s *Sim) ShiftPaused(d float64) {
	s.CountdownEnd += d
	for _, i := range s.Occupied() {
		b := s.Players[i]
		if b.Prot != 0 {
			b.Prot += d
		}
		if b.Pending != nil {
			b.PendT += d
		}
	}
	for _, ap := range s.Acids {
		ap.Until += d
	}
	for _, f := range s.Fx {
		f.Until += d
	}
	if s.BloodUntil != 0 {
		s.BloodUntil += d
	}
}

// Step advances the simulation by dt. now is the current unix time.
func (s *Sim) Step(dt, now float64) {
	if s.Phase == "countdown" && now >= s.CountdownEnd {
		s.Phase = "playing"
		return
	}
	if s.Phase != "playing" {
		return
	}
	s.Time += dt
	blood := now < s.BloodUntil
	occ := s.Occupied()
	for _, i := range occ {
		b := s.Players[i]
		if b.Down || b.Pending != nil {
			continue
		}
		ix := math.Max(-1, math.Min(1, b.IX))
		iy := math.Max(-1, math.Min(1, b.IY))
		if n := math.Hypot(ix, iy); n > 1 {
			ix /= n
			iy /= n
		}
		b.X += ix * b.Speed * dt
		b.Y += iy * b.Speed * dt
	}
	var downs, ups []int
	for _, i := range occ {
		if s.Players[i].Down && !s.Players[i].Dead {
			downs = append(downs, i)
		} else if !s.Players[i].Down {
			ups = append(ups, i)
		}
	}
	for _, i := range downs {
		b := s.Players[i]
		b.Bleed -= dt
		if b.Bleed <= 0 {
			b.Dead = true
			continue
		}
		near := false
		for _, j := range ups {
			if math.Hypot(b.X-s.Players[j].X, b.Y-s.Players[j].Y) < 60 {
				near = true
				break
			}
		}
		if near {
			mult := s.Players[i].ReviveMult
			b.Revive += dt * mult / 3.0
			if b.Revive >= 1 {
				b.Down = false
				b.Revive = 0
				b.HP = b.MaxHP * 0.5
				b.Prot = now + 2
				for _, j := range ups {
					if math.Hypot(b.X-s.Players[j].X, b.Y-s.Players[j].Y) < 60 {
						if hasStr(s.Players[j].Arti, "a_medic") {
							hj := s.Players[j]
							hj.HP = math.Min(hj.MaxHP, hj.HP+25)
						}
					}
				}
			}
		}
	}
	if len(occ) > 0 {
		allDown := true
		for _, i := range occ {
			if !s.Players[i].Down {
				allDown = false
				break
			}
		}
		if allDown {
			s.Phase = "over"
			s.EventID++
			s.LastOver = &OverEv{Time: math.Round(s.Time*10) / 10, Kills: s.Kills, ID: s.EventID}
			return
		}
	}
	interval := math.Max(0.22, 1.1-s.Time*0.0016)
	if blood {
		interval *= 0.5
	}
	s.SpawnT -= dt
	if s.SpawnT <= 0 && len(s.Enemies) < EnemyCap {
		s.SpawnT = interval
		batch := 1 + int(s.Time/75)
		if len(occ)-1 > 0 {
			batch += len(occ) - 1
		}
		for range batch {
			if len(s.Enemies) < EnemyCap {
				s.Enemies = append(s.Enemies, s.SpawnEnemy(false, false, now))
			}
		}
	}
	s.EliteT -= dt
	if s.EliteT <= 0 {
		s.EliteT = EliteEvery
		if len(s.Enemies) < EnemyCap {
			s.Enemies = append(s.Enemies, s.SpawnEnemy(true, false, now))
		}
	}
	s.BossT -= dt
	if s.BossT <= 0 {
		s.BossT = BossEvery
		s.BossN++
		s.Enemies = append(s.Enemies, s.SpawnEnemy(false, true, now))
		s.EventID++
		s.LastBoss = &BossEv{N: s.BossN, ID: s.EventID}
	}
	if s.Time >= s.BloodNext {
		s.BloodNext += 180.0
		s.BloodUntil = now + 30.0
		s.EventID++
		s.LastMoon = &IdEv{ID: s.EventID}
	}
	if s.rng.Float64() < dt/50 {
		n := 0
		for _, e := range s.Enemies {
			if e.Kind == "goblin" {
				n++
			}
		}
		if n < 1 && len(s.Enemies) < EnemyCap {
			s.Enemies = append(s.Enemies, s.SpawnGoblin(now))
			s.EventID++
			s.LastGoblin = &IdEv{ID: s.EventID}
		}
	}
	kept := s.Enemies[:0]
	for _, e := range s.Enemies {
		if e.Boss || e.Dead || s.NearAny(e.X, e.Y, DespawnR) {
			kept = append(kept, e)
		}
	}
	s.Enemies = kept
	keptG := s.Gems[:0]
	for _, gm := range s.Gems {
		if s.NearAny(gm.X, gm.Y, GemDespawnR) {
			keptG = append(keptG, gm)
		}
	}
	s.Gems = keptG
	s.weapons(dt, now, occ)
	s.stepShots(dt, now)
	s.stepAxes(dt, now)
	for _, ap := range s.Acids {
		ap.Tick -= dt
		if ap.Tick <= 0 {
			ap.Tick = 0.5
			for _, e := range s.Enemies {
				if e.Dead {
					continue
				}
				if math.Hypot(e.X-ap.X, e.Y-ap.Y) < ap.R {
					s.HurtEnemy(e, ap.DPS*0.5, ap.Owner, now)
				}
			}
		}
	}
	keptA := s.Acids[:0]
	for _, ap := range s.Acids {
		if now < ap.Until {
			keptA = append(keptA, ap)
		}
	}
	s.Acids = keptA
	keptF := s.Fx[:0]
	for _, f := range s.Fx {
		if now < f.Until {
			keptF = append(keptF, f)
		}
	}
	s.Fx = keptF
	var free []int
	for _, i := range occ {
		if !s.Players[i].Down && s.Players[i].Pending == nil {
			free = append(free, i)
		}
	}
	for _, e := range s.Enemies {
		if e.Dead {
			continue
		}
		if e.KX != 0 || e.KY != 0 {
			e.X += e.KX * dt
			e.Y += e.KY * dt
			damp := math.Max(0, 1-math.Min(1, dt*7))
			e.KX *= damp
			e.KY *= damp
		}
		if len(free) == 0 {
			continue
		}
		tgt := free[0]
		bd := math.MaxFloat64
		for _, i := range free {
			if d := math.Hypot(e.X-s.Players[i].X, e.Y-s.Players[i].Y); d < bd {
				tgt, bd = i, d
			}
		}
		b := s.Players[tgt]
		d := math.Hypot(b.X-e.X, b.Y-e.Y)
		if d == 0 {
			d = 1
		}
		spd := e.Spd
		if now < e.SlowUntil {
			spd *= 0.6
		}
		if e.Kind == "goblin" {
			if now > e.Despawn {
				e.Dead = true
				continue
			}
			e.X -= (b.X-e.X)/d*spd*dt
			e.Y -= (b.Y-e.Y)/d*spd*dt
			continue
		}
		dmg := e.Dmg
		if e.Kind == "charger" {
			switch e.Mode {
			case "chase":
				e.X += (b.X-e.X)/d*spd*dt
				e.Y += (b.Y-e.Y)/d*spd*dt
				if d < 380 && now >= e.RestUntil {
					e.Mode = "wind"
					e.ModeT = 0.7
					e.DX, e.DY = (b.X-e.X)/d, (b.Y-e.Y)/d
				}
			case "wind":
				e.ModeT -= dt
				if e.ModeT <= 0 {
					e.Mode = "dash"
					e.ModeT = 0.5
				}
			case "dash":
				e.X += e.DX*520*dt
				e.Y += e.DY*520*dt
				dmg = e.Dmg * 1.5
				e.ModeT -= dt
				if e.ModeT <= 0 {
					e.Mode = "chase"
					e.RestUntil = now + 2.0
				}
			default:
				e.X += (b.X-e.X)/d*spd*dt
				e.Y += (b.Y-e.Y)/d*spd*dt
			}
		} else {
			e.X += (b.X-e.X)/d*spd*dt
			e.Y += (b.Y-e.Y)/d*spd*dt
		}
		if d < e.R+18 && now >= e.Atk {
			e.Atk = now + 0.8
			hp0 := b.HP
			s.HurtPlayer(tgt, dmg, now)
			if b.HP < hp0 {
				s.EventID++
				s.LastHurt = &HurtEv{Seat: tgt + 1, Amt: math.Round((hp0-b.HP)*10) / 10,
					Fx: math.Round(e.X*10) / 10, Fy: math.Round(e.Y*10) / 10, ID: s.EventID}
			}
			if b.Thorns != 0 {
				s.HurtEnemy(e, float64(b.Thorns), tgt, now)
			}
		}
	}
	keptE := s.Enemies[:0]
	for _, e := range s.Enemies {
		if !e.Dead {
			keptE = append(keptE, e)
		}
	}
	s.Enemies = keptE
	if len(s.Gems) > GemCap {
		sortGems(s.Gems)
		s.Gems = s.Gems[len(s.Gems)-GemCap:]
	}
	for _, gm := range s.Gems {
		best, bd := -1, 1e9
		for _, i := range occ {
			b := s.Players[i]
			if b.Down {
				continue
			}
			if d := math.Hypot(gm.X-b.X, gm.Y-b.Y); d < bd {
				best, bd = i, d
			}
		}
		if best < 0 {
			continue
		}
		b := s.Players[best]
		if bd < b.Magnet {
			if bd > 1 {
				gm.X += (b.X-gm.X)/bd*420*dt
				gm.Y += (b.Y-gm.Y)/bd*420*dt
			}
		}
		if bd < 24 {
			gm.Dead = true
			s.GainXp(best, gm.V, now)
		}
	}
	keptG2 := s.Gems[:0]
	for _, gm := range s.Gems {
		if !gm.Dead {
			keptG2 = append(keptG2, gm)
		}
	}
	s.Gems = keptG2
	for _, i := range occ {
		b := s.Players[i]
		if b.Pending != nil && now >= b.PendT {
			s.ApplyPick(i, b.Pending[0].ID)
			b.Pending = nil
		}
	}
}

// sortGems sorts ascending by value for cap trimming (keeps richest).
func sortGems(gems []*Gem) {
	for i := 1; i < len(gems); i++ {
		for j := i; j > 0 && gems[j-1].V > gems[j].V; j-- {
			gems[j-1], gems[j] = gems[j], gems[j-1]
		}
	}
}
