@echo off
REM ================================================
REM  PowerCodeDeck - Windows Installer (double-click)
REM ================================================
REM Double-clicking install.ps1 opens Notepad or is blocked by the
REM PowerShell execution policy, so run it explicitly with a bypass.

setlocal
set "SCRIPT_DIR=%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT_DIR%install.ps1"
if errorlevel 1 (
    echo.
    echo   Installation failed. See the errors above.
    pause
)
endlocal
