# Echo Hunt — LAN 2–8P sonar tag 🛰️

Fullscreen browser game on a 2200×1400 trench (tap ⛶, landscape best — the camera follows you). One phone per player. **Wardens** hunt on a near-black screen with only sonar blips; **Divers** see a wide circle of the trench and must crack wire nodes to win the round.

- Hunters scale with the table: `max(1, round(players/3))` — 1v1, 1v2 … up to 3v5. Roles rotate every round (least-recent hunters get the armband). Fresh mirrored map pressure every match — pillars block sight and shells of sound alike.
- Sonar auto-pings every 4s. Your stick is analog: stroll = silent, sprint = loud and bright. Divers see the rings coming — freeze to ghost them.
- **Terror radius:** divers hear a heartbeat that quickens as a warden closes in (380px), with blood-red edges. If you hear it, RUN.
- Sprinting drains stamina (~4s tank); empty = forced stroll until it recovers.
- Nodes take 3 wire puzzles each (3–8 color pairs, match-only). Touch 2s to claim a puzzle, solve all 3 to crack the node. Claiming, solving, and cracking all shout on sonar. Two divers can work different puzzles on the same node. Move (or 🏃 RUN) abandons your puzzle, banked count kept.
- Round win: crack the target (`4 + #divers`, so 6 at classic 1v2), tag out every diver, or survive the 150s timer (divers). First side to 3 round-wins takes the match.

## Run

```bash
python3 server.py        # serves on :3003 by default (PORT=xxxx to change)
```

Host opens `http://localhost:3003`, friends open `http://<host-ip>:3003`. Enter names → Join → Ready (need 2+, max 8, extras spectate).

Short table? The host (localhost device) can add 🤖 bot players from the lobby (+/−, host only). Bots take seats like anyone, get roles by the same rotation, and play with exactly their role's information — diver bots see nodes but only hunters inside their fog, warden bots hunt sonar blips and stumble into divers up close. They solve wire puzzles slowly, shout like anyone, and get tagged out like anyone.

## Controls

- Touch: drag anywhere to swim (push far = sprint, easy = stroll)
- No buttons — deflection *is* the stealth mechanic. Wardens steer the same way.
