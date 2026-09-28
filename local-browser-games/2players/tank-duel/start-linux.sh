#!/bin/bash
# Tank Duel — starts the server and opens the game. Ctrl+C stops it.
cd "$(dirname "$0")"
PORT=3001
python3 server.py &
SRV=$!
trap "kill $SRV 2>/dev/null" EXIT INT TERM
for i in $(seq 1 60); do
  (echo > /dev/tcp/127.0.0.1/$PORT) 2>/dev/null && break
  sleep 0.1
done
xdg-open "http://localhost:$PORT" 2>/dev/null || echo "Open http://localhost:$PORT in your browser"
wait $SRV
