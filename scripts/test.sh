#!/bin/sh
# scripts/test.sh
# 
# Automates running the Go test suite inside an ephemeral Docker container.
# This ensures tests are run in an environment identical to the builder stage
# (Alpine Linux, CGO enabled, SQLite dependencies present) without requiring
# a local Go toolchain on the host machine.
#
# Usage: ./scripts/test.sh

echo "Running tests in Docker container (golang:1.23-alpine)..."

docker run --rm \
  -v "$(pwd)":/src \
  -w /src \
  golang:1.23-alpine \
  sh -c "apk add --no-cache gcc musl-dev > /dev/null && CGO_ENABLED=1 go test -v ./..."
