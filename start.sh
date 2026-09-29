#!/bin/bash
set -e

echo "=================================================="
echo "🐾 Starting PawSOS Emergency Animal Rescue Platform"
echo "=================================================="

# Check if precompiled Go binary exists
if [ -f "./pawsos" ]; then
    echo "Starting compiled PawSOS production server on http://localhost:8080 ..."
    (sleep 1 && (xdg-open http://localhost:8080 2>/dev/null || open http://localhost:8080 2>/dev/null || true)) &
    exec ./pawsos
elif command -v go &>/dev/null; then
    echo "Compiling and starting PawSOS Go server..."
    go build -ldflags="-s -w" -o pawsos ./cmd/server
    (sleep 1 && (xdg-open http://localhost:8080 2>/dev/null || open http://localhost:8080 2>/dev/null || true)) &
    exec ./pawsos
elif command -v python3 &>/dev/null; then
    echo "Warning: Go compiler not detected. Falling back to static web view..."
    (sleep 1 && (xdg-open http://localhost:8080 2>/dev/null || open http://localhost:8080 2>/dev/null || true)) &
    exec python3 -m http.server 8080
else
    echo "Launching web view directly in browser..."
    xdg-open index.html 2>/dev/null || open index.html 2>/dev/null
fi
