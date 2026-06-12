@echo off
title Claude Auto-Resume (one-time run)
echo.
echo Checking your Claude usage limit. If it's active, this window will
echo wait for the reset and then resume your recent sessions.
echo Leave this window open. Close it any time to cancel.
echo.
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0Claude-AutoResume.ps1"
echo.
pause
