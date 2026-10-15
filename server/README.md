# Fetcharr server

Self-hosted download server. The Fetcharr Android app (or any HTTP client) sends it a URL plus a target category; the server downloads it with yt-dlp, post-processes it with ffmpeg and files it into your media library using Jellyfin/Plex-friendly names, then optionally asks your media server to rescan.

It is a single static Go binary (no CGO, SQLite embedded). At runtime it needs `ffmpeg` and `ffprobe` on `PATH`. yt-dlp is downloaded and kept up to date by the server itself unless you point it at your own binary.

Full behaviour and the HTTP/WebSocket API are specified in [`docs/fetcharr-server.md`](../docs/fetcharr-server.md).

## Quick start (Docker)

1. Create a config and the folders to mount:

   ```sh
   mkdir -p config data
   cp config.example.yaml config/config.yaml   # edit categories and token
   ```

   The category folders (`/media/movies`, ...) and `data_dir` (`/data`) must already exist inside the container and be writable by the container user.

2. Run it (the image runs as UID/GID 1000 by default; pass `--user` to match the owner of your media folders):

   ```sh
   docker run -d --name fetcharr \
     --user 1000:1000 \
     -p 8080:8080 \
     -v "$PWD/config:/config" \
     -v "$PWD/data:/data" \
     -v /mnt/media:/media \
     --restart unless-stopped \
     ghcr.io/istarvin/fetcharr-server:latest
   ```

   Or with Compose:

   ```yaml
   services:
     fetcharr:
       image: ghcr.io/istarvin/fetcharr-server:latest
       user: "1000:1000"          # must be able to write to /data and the media folders
       ports: ["8080:8080"]
       environment:
         FETCHARR_CONFIG: /config/config.yaml
       volumes:
         - ./config:/config
         - ./data:/data
         - /mnt/media:/media
       restart: unless-stopped
   ```

3. Check it: `curl http://localhost:8080/api/health`. The first start downloads yt-dlp into `/data/bin` and aborts if that download fails, so the container needs internet access on first start (or set `ytdlp.binary` to a binary you mount in).

The image is Debian-based (glibc) so the standalone yt-dlp release binary runs without Python. It includes a `HEALTHCHECK` on `/api/health` at port 8080. If you change `server.port` in the config, keep it at 8080 inside the container and remap with `ports:` (or override the healthcheck).

Build locally with `docker build -t fetcharr-server server/` (optional `--build-arg VERSION=1.2.3`, reported by `GET /api/info`).

## Configuration

Copy [`config.example.yaml`](config.example.yaml) and edit it. The file is selected with `--config <path>`, `FETCHARR_CONFIG`, or defaults to `./config.yaml`. Unknown keys are rejected, and the server refuses to start with a clear error if a category path is missing or not writable, `data_dir` is not writable, or `ffmpeg`/`ffprobe` are missing. Details: [spec section 3](../docs/fetcharr-server.md#3-configuration).

Set `server.auth_token` if the server is reachable by anyone you do not trust; clients then send `Authorization: Bearer <token>`.

## Bare metal

Requirements: Go (version in `go.mod`) to build, plus `ffmpeg` and `ffprobe` on `PATH` at runtime.

```sh
cd server
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o fetcharr ./cmd/fetcharr
./fetcharr --config /etc/fetcharr/config.yaml
```

For a long-running install use the systemd unit in [`deploy/fetcharr.service`](deploy/fetcharr.service); the install steps are in its header. Set `ReadWritePaths=` to your library folders and make sure the `fetcharr` user can write to them and to `data_dir`.

## API

REST under `/api`, plus a WebSocket for live job updates. See [`docs/fetcharr-server.md`](../docs/fetcharr-server.md) (sections 6 and 7) and the wire fixtures in [`contract/`](../contract). `scripts/smoke.sh` exercises a running server end to end:

```sh
BASE_URL=http://127.0.0.1:8080 TOKEN=... scripts/smoke.sh
```

## Development

```sh
cd server
go vet ./...
go test ./...
golangci-lint run        # config in .golangci.yml
```

Tests never touch the real yt-dlp. [`testdata/fake-ytdlp`](testdata/README.md) is a python3 stand-in; set `ytdlp.binary` to it (unmanaged mode) to run the server locally without network access, with `FAKE_YTDLP_DURATION` controlling how long a fake download takes.

## Release

Pushing a tag `server-vX.Y.Z` runs `.github/workflows/server-release.yml`, which builds a multi-arch (`linux/amd64`, `linux/arm64`) image and pushes `ghcr.io/istarvin/fetcharr-server` with tags `X.Y.Z`, `X.Y`, `X` and `latest` (pre-release tags such as `server-v1.2.0-rc.1` get only the full version tag).

```sh
git tag server-v1.0.0 && git push origin server-v1.0.0
```
