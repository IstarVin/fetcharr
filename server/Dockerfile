# syntax=docker/dockerfile:1

# ---- build stage ------------------------------------------------------------
FROM golang:1.26-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# VERSION is reported by GET /api/info (main.version).
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/fetcharr ./cmd/fetcharr

# ---- runtime stage ----------------------------------------------------------
# Debian (glibc) rather than Alpine: the managed yt-dlp standalone binary
# (yt-dlp_linux / yt-dlp_linux_aarch64) is a PyInstaller bundle that bundles its
# own Python, so no python3 is needed here. ffmpeg provides ffprobe too.
# curl is only used by HEALTHCHECK; tini reaps yt-dlp/ffmpeg child processes.
FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ffmpeg ca-certificates curl tini \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --gid 1000 fetcharr \
 && useradd --uid 1000 --gid 1000 --no-create-home --home-dir /tmp --shell /usr/sbin/nologin fetcharr \
 && mkdir -p /data /config \
 && chown fetcharr:fetcharr /data

COPY --from=build /out/fetcharr /usr/local/bin/fetcharr

ENV FETCHARR_CONFIG=/config/config.yaml \
    HOME=/tmp

# Override with `user:` / `--user` to match the owner of your media folders.
USER 1000:1000
VOLUME /data
EXPOSE 8080

# start-period covers the first-start yt-dlp download in managed mode.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5m --retries=3 \
  CMD curl -fsS http://127.0.0.1:8080/api/health || exit 1

ENTRYPOINT ["tini", "--", "fetcharr"]
