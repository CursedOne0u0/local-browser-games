@echo off
title Bomb Tag server — close this window or press Ctrl+C to stop
echo Bomb Tag. To stop it: close this window or press Ctrl+C.
where python >nul 2>nul
if errorlevel 1 (
  echo.
  echo ERROR: Python not found. Install it from https://www.python.org/downloads/
  echo and tick "Add python.exe to PATH" during setup, then double-click this again.
  pause
  exit /b 1
)

REM Bomb Tag — starts the server and opens the game. Needs Python installed.
cd /d "%~dp0"
start "" /min cmd /c "timeout /t 2 /nobreak >nul & start http://localhost:3002"
python server.py
