@echo off
REM Neon Pong Showdown — starts the server and opens the game. Needs Python installed.
cd /d "%~dp0"
start "" /min cmd /c "timeout /t 2 /nobreak >nul & start http://localhost:3000"
python server.py
