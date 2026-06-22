# ── Stage 1: build ──────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

# ca-certificates needed for TLS calls (embed API, Postgres TLS)
RUN apk add --no-cache ca-certificates git

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /mempalace \
    ./cmd/mempalace

# ── Stage 2: runtime ────────────────────────────────────────────────────────
FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata

# Non-root user
RUN addgroup -S app && adduser -S -G app app
USER app

COPY --from=builder /mempalace /usr/local/bin/mempalace

EXPOSE 8000

ENTRYPOINT ["/usr/local/bin/mempalace"]
