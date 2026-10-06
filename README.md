# 🎮 Local Browser Games

Zero installs, zero internet needed. Static games open straight from `index.html`;
LAN games run from a single Go binary or fall back to Python (see below).

**Play Breakout Blast and Pong Duo right now, no download:** ▶ 👉 [Play in the browser](https://benjooyt.github.io/browser-games/)

**Easiest local start:** each game folder has `start-linux.sh` (double-click or `./start-linux.sh`, Ctrl+C stops the server) and `start-windows.bat` (double-click) — they boot the server (LAN games) and open the game in your browser.

## gameserver — one binary for all LAN games

`gameserver/bin/` holds prebuilt servers (`gameserver-linux-amd64`, `gameserver-linux-arm64`, `gameserver.exe` — no runtime needed). Run it, pick a game from the menu (`1`–`6`, or `a` for all), or launch directly: `gameserver -game 3 -port 3002` (`-no-browser` skips auto-open). Each game's `start-linux.sh` uses the Go binary when present and falls back to `python3 server.py` otherwise — same protocol, same client.

## 1player — solo, just open `index.html`

### Breakout Blast

<img src="local-browser-games/1player/breakout-blast/screenshot.png" width="500">

`1player/breakout-blast/` — brick-breaker with combos, bombs, lasers and shields.

### Neon Horde Solo

<img src="local-browser-games/1player/neon-horde-solo/shot-v2.png" width="500">

`1player/neon-horde-solo/` — solo survivors: pause-drafts, death ends the run, best time saved.

### Neon Drift

<img src="local-browser-games/1player/neon-drift/screenshot.png" width="500">

`1player/neon-drift/` — top-down time-trial racer: fresh seeded track every day, per-day top runs, exportable ghost replays.

## 2players — LAN duel: host runs the server, both open the printed `http://<host-ip>:<port>` URL on the same Wi-Fi

### Tank Duel

<img src="local-browser-games/2players/tank-duel/screenshot.png" width="500">

`2players/tank-duel/` — maze tanks, bouncing shells, weapon pickups → `:3001`

### Bomb Tag

<img src="local-browser-games/2players/bomb-tag/screenshot.png" width="500">

`2players/bomb-tag/` — hot-potato chase, dash escapes → `:3002`

### Echo Hunt

<img src="local-browser-games/2players/echo-hunt/screenshot.png" width="500">

`2players/echo-hunt/` — 2–8P sonar tag: near-blind wardens hunt on pings, divers crack wire nodes → `:3003`

### Neon Horde

<img src="local-browser-games/2players/neon-horde/screenshot.png" width="500">

`2players/neon-horde/` — 2–4 hunter co-op survivors, endless horde, per-player drafts, boss every 4 min → `:3004`

### Bastion LAN

<img src="local-browser-games/2players/bastion/screenshot.png" width="500">

`2players/bastion/` — 2–4 defender co-op tower defense on separate screens, shared gold, base has 20 HP → `:3005`

## hybrid1-2 — solo or 2-player

### Neon Pong Showdown

<img src="local-browser-games/hybrid1-2/neon-pong/screenshot.png" width="500">

`hybrid1-2/neon-pong/` — power-up Pong, 2P LAN + solo vs AI → `:3000`

### Pong Duo

<img src="local-browser-games/hybrid1-2/pong-duo/screenshot.png" width="500">

`hybrid1-2/pong-duo/` — solo vs AI or 2P on one keyboard, just open `index.html`

### Swarm Survival

<img src="local-browser-games/hybrid1-2/swarm/screenshot.png" width="500">

`hybrid1-2/swarm/` — co-op arena survival (move + autofire only): 50s waves, drafts, bombs, revive your partner, just open `index.html`

### Bastion Bros

<img src="local-browser-games/hybrid1-2/bastion/screenshot.png" width="500">

`hybrid1-2/bastion/` — co-op tower defense, you are the build cursor: solo, solo + AI companion, or 2P on one keyboard, just open `index.html`
