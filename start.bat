@echo off
echo ==================================================
echo PawSOS Emergency Animal Rescue Platform
echo ==================================================

if exist "pawsos.exe" (
    echo Starting compiled PawSOS binary on http://localhost:8080 ...
    start http://localhost:8080
    pawsos.exe
    goto end
)

where go >nul 2>nul
if %ERRORLEVEL% EQU 0 (
    echo Compiling and starting PawSOS Go server...
    start http://localhost:8080
    go run ./cmd/server
    goto end
)

where python >nul 2>nul
if %ERRORLEVEL% EQU 0 (
    echo Warning: Go not found, launching with Python...
    start http://localhost:8080
    python -m http.server 8080
    goto end
)

echo Opening index.html in your default web browser...
start index.html

:end
