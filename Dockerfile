# ──────────────────────────────────────────────
# Stage 1: Build the Go binary
# ──────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

# Install C compiler for CGO (required by go-sqlite3)
RUN apk add --no-cache gcc musl-dev

WORKDIR /src

# Cache dependency downloads
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true

# Copy source and build
COPY . .
RUN CGO_ENABLED=1 go build -o /bin/bap ./cmd/main.go

# ──────────────────────────────────────────────
# Stage 2: Minimal production image
# ──────────────────────────────────────────────
FROM alpine:3.20

# Runtime C library needed by CGO-compiled binary
RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /bin/bap /app/bap

ENTRYPOINT ["/app/bap"]
CMD ["serve"]
