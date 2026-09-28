@echo off
title Echo Hunt server — close this window or press Ctrl+C to stop
echo Echo Hunt. To stop it: close this window or press Ctrl+C.
where python >nul 2>nul
if errorlevel 1 (
  echo.
  echo ERROR: Python not found. Install it from https://www.python.org/downloads/
  echo and tick "Add python.exe to PATH" during setup, then double-click this again.
  pause
  exit /b 1
)

REM Echo Hunt — starts the server and opens the game. Needs Python installed.
cd /d "%~dp0"
start "" /min cmd /c "timeout /t 2 /nobreak >nul & start http://localhost:3003"
python server.py
