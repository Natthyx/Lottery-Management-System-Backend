# syntax=docker/dockerfile:1.7

# ── Stage 1: Build ───────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Cache dependency downloads separately from source.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# CGO_ENABLED=0 → fully static binary
# -trimpath          → strip workspace paths from binary
# -ldflags -w -s     → drop DWARF + symbol tables for smaller binary
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build \
      -trimpath \
      -ldflags="-w -s -X main.version=${VERSION}" \
      -o /out/server \
      ./cmd/server

# ── Stage 2: Runtime ─────────────────────────────────────────
# distroless = no shell, minimal attack surface, non-root user
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/server /server

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/server"]
