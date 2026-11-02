@echo off
chcp 65001 >nul 2>&1
setlocal
cd /d "%~dp0"
"%~dp0artex-launcher.exe" %*
if errorlevel 1 pause
