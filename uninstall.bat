@echo off
title Claude Auto-Resume Uninstall
echo.
echo Removing Claude Auto-Resume...
echo.
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0Install-AutoResumeTask.ps1" -Uninstall
echo.
pause
