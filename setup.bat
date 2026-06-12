@echo off
title Claude Auto-Resume Setup
echo.
echo Installing Claude Auto-Resume (background watcher)...
echo.
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0Install-AutoResumeTask.ps1"
if errorlevel 1 (
    echo.
    echo Something went wrong. Try right-clicking setup.bat and choosing
    echo "Run as administrator", then try again.
)
echo.
pause
