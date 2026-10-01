# Bomb Tag — LAN 2P 💣

![Gameplay](screenshot.png)

Hot potato in an arena. One ticks, all run — tag to pass. The holder is slower. Dash has a 3s cooldown. Random 8–14s fuse. 2P: rival scores, first to 5. 3–4P: everyone starts at 5, first to 0 loses.

## Run

```bash
python3 server.py        # serves on :3002 by default (PORT=xxxx to change)
```

Host opens `http://localhost:3002`, friend opens `http://<host-ip>:3002`. Enter names → Join → Ready.

## Controls

- `WASD`/arrows to run, `Space`/`E` to dash
- Touch: drag to run, right-side button to dash
