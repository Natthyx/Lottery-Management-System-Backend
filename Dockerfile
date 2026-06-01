# Stage 1: Build — produces a static binary
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git
WORKDIR /app

# Cache dependency downloads separately from source changes
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 = fully static binary (no libc)
# -ldflags="-w -s" = strip debug symbols (smaller binary)
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s" \
    -o /app/server \
    ./cmd/server

# Stage 2: Runtime — distroless = no shell, minimal attack surface
FROM gcr.io/distroless/static-debian12

COPY --from=builder /app/server /server

USER 65534
EXPOSE 8080
ENTRYPOINT ["/server"]
