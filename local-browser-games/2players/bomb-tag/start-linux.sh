#!/bin/bash
# Bomb Tag — start|stop|restart|status. Opens the game on start. Ctrl+C stops it.
cd "$(dirname "$0")"
PORT=3002
PIDF=.server.pid
alive(){ [ -n "$1" ] && ps -p "$1" >/dev/null 2>&1 && tr '\0' ' ' < "/proc/$1/cmdline" 2>/dev/null | grep -q "server.py"; }
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
    # keep the host CPU awake while serving (Termux:API only; harmless elsewhere)
    command -v termux-wake-lock >/dev/null 2>&1 && termux-wake-lock
    python3 server.py &
    SRV=$!; echo $SRV > "$PIDF"
    trap "kill $SRV 2>/dev/null; rm -f $PIDF; command -v termux-wake-unlock >/dev/null 2>&1 && termux-wake-unlock" EXIT INT TERM
    for i in $(seq 1 60); do
      (echo > /dev/tcp/127.0.0.1/$PORT) 2>/dev/null && break
      sleep 0.1
    done
    xdg-open "http://localhost:$PORT" 2>/dev/null || echo "Open http://localhost:$PORT in your browser"
    wait $SRV
    ;;
esac
