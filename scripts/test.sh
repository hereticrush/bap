#!/bin/sh
# scripts/test.sh
# 
# Automates running the Go test suite inside an ephemeral Docker container.
# This ensures tests are run in an environment identical to the builder stage
# (Alpine Linux, CGO enabled, SQLite dependencies present) without requiring
# a local Go toolchain on the host machine.
#
# Usage: ./scripts/test.sh

if [ -z "$GOPATH" ]; then
  echo "GOPATH is not set. Please set it before running this script."
  exit 1
fi

echo "Running tests in Docker container (golang:1.24-alpine)..."

docker run --rm \
  -v "$(pwd)":/src \
  -w /src \
  golang:1.24-alpine \
  sh -c "apk add --no-cache gcc musl-dev > /dev/null && go mod download && CGO_ENABLED=1 go test -v ./..."
