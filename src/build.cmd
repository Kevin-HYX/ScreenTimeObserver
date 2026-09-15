@echo off
setlocal
cd /d "%~dp0"
echo building collector.exe (windowsgui, no console window) ...
go build -buildvcs=false -ldflags "-s -w -H=windowsgui" -o "..\collector.exe" .
if errorlevel 1 goto :fail
echo building collector-cli.exe (console, for CLI subcommands) ...
go build -buildvcs=false -ldflags "-s -w" -o "..\collector-cli.exe" .
if errorlevel 1 goto :fail
echo done.
exit /b 0
:fail
echo BUILD FAILED
exit /b 1