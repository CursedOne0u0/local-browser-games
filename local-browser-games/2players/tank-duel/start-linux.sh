#!/bin/bash
# Tank Duel — start|stop|restart|status. Opens the game on start. Ctrl+C stops it.
cd "$(dirname "$0")"
PORT=3001
PIDF=.server.pid
alive(){ [ -n "$1" ] && ps -p "$1" >/dev/null 2>&1 && tr '\0' ' ' < "/proc/$1/cmdline" 2>/dev/null | grep -qE "server.py|gameserver"; }
stop_srv(){
  if [ -f "$PIDF" ]; then
    PID=$(cat "$PIDF"); rm -f "$PIDF"
    if alive "$PID"; then kill "$PID" 2>/dev/null; echo "stopped $PID"; else echo "not running"; fi
  else
    echo "not running"
  fi
}
case "${1:-start}" in
  stop) stop_srv;;
  status) if [ -f "$PIDF" ] && alive "$(cat "$PIDF")"; then echo "running ($(cat "$PIDF")) on :$PORT"; else echo "not running"; fi;;
  restart) stop_srv; sleep 1; exec "$0" start;;
  *) # start (clears any stale instance first, so rerun == restart)
    stop_srv >/dev/null 2>&1
    # prefer the prebuilt Go server; fall back to python3 server.py
    GOBIN=""
    case "$(uname -sm)" in
      "Linux x86_64") GOBIN="../../../gameserver/bin/gameserver-linux-amd64";;
      "Linux aarch64"|"Linux arm64") GOBIN="../../../gameserver/bin/gameserver-linux-arm64";;
    esac
    if [ -n "$GOBIN" ] && [ -x "$GOBIN" ]; then
      "$GOBIN" -game 2 -port "$PORT" &
    else
      python3 server.py &
    fi
    SRV=$!; echo $SRV > "$PIDF"
    trap "kill $SRV 2>/dev/null; rm -f $PIDF" EXIT INT TERM
    for i in $(seq 1 60); do
      (echo > /dev/tcp/127.0.0.1/$PORT) 2>/dev/null && break
      sleep 0.1
    done
    xdg-open "http://localhost:$PORT" 2>/dev/null || echo "Open http://localhost:$PORT in your browser"
    wait $SRV
    ;;
esac
