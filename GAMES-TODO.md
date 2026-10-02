# Game ideas backlog

Three concepts, each: static/local co-op (solo + AI companion, same-keyboard 2P)
first, LAN version later. Conventions for all: single self-contained
`index.html`, dt-based loop, WebAudio beeps, pause veil, gamepad shim,
PWA pack for statics, `game.json` entry, 1000×800 action thumbnail.

---

## 1. Curve Fever (versus party)

*Achtung-die-Kurve* clone. 2–4 snakes on one screen, permanent trails,
last alive wins. Random gap windows let you cross lines (and fake out
opponents). Solo vs greedy wall-avoidance AI; local 2P on split keys
(WASD / arrows), pads 3–4. LAN later: input-relay server like bomb-tag
(turn state is tiny: headings + trail points).

- Controls: single turn key per player (left/right) — simplest input in
  the collection.
- Modes: free-for-all rounds, first to 5 round-wins.
- Scope notes: trail collision on a coarse grid (cell 8px), gap RNG with
  pity timer so nobody gets walled unfairly.

## 2. Bastion Bros (co-op tower defense)

Shared-gold build phase (walls + arrow/cannon/frost towers, both players
spend from one pool), then wave phase (kite leakers, repair between
waves). Solo + AI companion (AI builds near breaches, repairs), same
keyboard (P1 build/move, P2 build/move — shared cursor would fight, so
split hotkeys: P1 `1-4` + WASD, P2 `7-0` + arrows). LAN later: grid +
gold server-side, clients send build/move intents.

- Win/lose: 10 waves, base has 20 HP, leaks cost by enemy size.
- Scope notes: grid 16×10, 3 tower types max for v1, no maze-validation
  beyond "path must exist" flood check.

## 3. Slingshot Golf (gravity duel)

Real-time projectile duel around gravity wells: fling shots into the
goal ring; scoring by proximity + goals. Same screen (P1 aims with mouse/
WASD power, P2 arrows), solo vs AI that integrates the field numerically.
LAN later: lock-step shots, server owns well layout.

- Win: 9 holes, lowest total strokes/distance.
- Scope notes: fixed-timestep gravity sim, 2–3 wells per hole from a
  seeded set, predicted-trajectory dotted preview (the fun part).
