#!/bin/bash
# Breakout Blast (solo) — opens the game in your browser. No server needed.
cd "$(dirname "$0")"
xdg-open "index.html" 2>/dev/null || echo "Open index.html in your browser"
