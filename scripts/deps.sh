#!/bin/sh
# scripts/deps.sh
# 
# Fetches Go dependencies and runs go mod tidy inside an ephemeral Docker container.
# This ensures dependencies are updated without requiring a local Go toolchain.
#
# Usage: ./scripts/deps.sh

echo "Fetching dependencies in Docker container (golang:1.24-alpine)..."

docker run --rm \
  -v "$(pwd)":/src \
  -w /src \
  golang:1.24-alpine \
  sh -c "apk add --no-cache gcc musl-dev > /dev/null && go get github.com/mattn/go-sqlite3 && go get github.com/hibiken/asynq && go get golang.org/x/oauth2@v0.24.0 google.golang.org/api@v0.200.0 github.com/alicebob/miniredis/v2 && go mod tidy"
