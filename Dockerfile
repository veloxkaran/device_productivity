# ─────────────────────────────────────────────────────────────────────────────
# Hajir Activity Hub — container image
#
# Builds the pure-Go hub server (no cgo) and ships it in a minimal image.
# The desktop AGENT is NOT containerised — it runs on each employee's computer.
# This image runs:  ./my-monitor hub   (listens on :4010)
#
#   docker build -t hajir-hub .
#   docker run -p 4010:4010 -v hajir-hub-data:/data hajir-hub
# ─────────────────────────────────────────────────────────────────────────────

# ── Build stage ──────────────────────────────────────────────────────────────
FROM golang:1.22-bookworm AS build
WORKDIR /src

# Cache modules first
COPY go.mod go.sum ./
RUN go mod download

# Build the static binary. CGO off (modernc SQLite is pure Go); timetzdata
# embeds the IANA tz database so time zones work without a tzdata package.
COPY . .
ARG HAJIR_API_URL=""
ARG HUB_URL=""
RUN CGO_ENABLED=0 GOOS=linux go build \
      -tags timetzdata \
      -ldflags="-s -w" \
      -o /out/my-monitor .

# ── Runtime stage ────────────────────────────────────────────────────────────
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget \
 && adduser -D -H -u 10001 hajir \
 && mkdir -p /data && chown hajir:hajir /data
WORKDIR /app
COPY --from=build /out/my-monitor /app/my-monitor

# Sensible container defaults (override at runtime with -e / compose).
ENV HUB_ADDR=":4010" \
    HUB_DATA_DIR="/data" \
    HUB_PUBLIC_URL="http://localhost:4010" \
    HAJIR_API_URL="http://host.docker.internal:8001/api/v2" \
    HUB_ALLOWED_ORIGINS="http://localhost:3000" \
    HUB_TIMEZONE="Asia/Kathmandu"

VOLUME ["/data"]
EXPOSE 4010
USER hajir

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:4010/health >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/app/my-monitor", "hub"]
