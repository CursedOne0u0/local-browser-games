@echo off
title Tank Duel server — close this window or press Ctrl+C to stop
echo Tank Duel. To stop it: close this window or press Ctrl+C.

REM Tank Duel — starts the server and opens the game. Needs Python installed.
cd /d "%~dp0"
start "" /min cmd /c "timeout /t 2 /nobreak >nul & start http://localhost:3001"
python server.py
