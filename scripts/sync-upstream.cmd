@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0sync-upstream.ps1" %*
exit /b %ERRORLEVEL%
