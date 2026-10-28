# syntax=docker/dockerfile:1
FROM golang:1.27-trixie AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-w -s -X main.Version=$VERSION" -o /app/bin/sigismundo ./cmd/sigismundo

FROM debian:13-slim
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt \
    dpkg --add-architecture i386 \
    && apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    ffmpeg \
    fonts-liberation \
    pulseaudio \
    wine \
    wine32:i386 \
    wine64 \
    xvfb \
    && rm -rf /var/lib/apt/lists/*
# Xvfb running as non-root can't create its socket dir itself.
RUN useradd -u 1000 -m -d /app sigismundo && mkdir /data /games && chown sigismundo /data \
    && mkdir -m 1777 /tmp/.X11-unix

COPY --link --from=builder /app/bin/sigismundo /usr/local/bin/sigismundo

ENV GO_ENV=production \
    DATA_DIR=/data \
    GAMES_DIR=/games \
    WINEPREFIX=/data/wine \
    AUDIO_MODE=spawn \
    SERVER_ADDRESS=0.0.0.0:8080
USER 1000
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["sigismundo"]
