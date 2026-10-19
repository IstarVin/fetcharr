# Fetcharr server

Self-hosted download server. The Fetcharr Android app, the built-in web UI (or any HTTP client) sends it a URL plus a target category; the server downloads it with yt-dlp, post-processes it with ffmpeg and files it into your media library using Jellyfin/Plex-friendly names, then optionally asks your media server to rescan.

It is a single static Go binary (no CGO, SQLite embedded). At runtime it needs `ffmpeg` and `ffprobe` on `PATH`. yt-dlp is downloaded and kept up to date by the server itself unless you point it at your own binary.

Full behaviour and the HTTP/WebSocket API are specified in [`docs/fetcharr-server.md`](../docs/fetcharr-server.md).

## Quick start (Docker)

1. From the repository root, create the deployment config and the folders to mount:

   ```sh
   mkdir -p .local/fetcharr/config .local/fetcharr/data .local/fetcharr/media
   cp server/config.minimal.yaml .local/fetcharr/config/config.yaml   # optionally set server.auth_token
   ```

   `data_dir` (`/data`) must already exist inside the container and be writable by the container user. Adding a category in Android creates missing media folders, including parent folders, if the container user has permission.

2. Build and start the server with [`compose.yaml`](../compose.yaml):

   ```sh
   FETCHARR_UID=$(id -u) FETCHARR_GID=$(id -g) docker compose up -d --build
   ```

   The command runs the container as your current UID/GID; Compose defaults to `1000:1000` when these variables are unset. The user must be able to read `.local/fetcharr/config/config.yaml` and write to the data and media folders. Set the user and host port through environment variables or a root `.env` file:

   ```sh
   FETCHARR_UID=$(id -u) FETCHARR_GID=$(id -g) \
     FETCHARR_PORT=8080 \
     docker compose up -d --build
   ```

   The default port is `8080`. Bind mounts map `.local/fetcharr/config`, `.local/fetcharr/data`, and `.local/fetcharr/media` to `/config`, `/data`, and `/media`. The data directory persists the database, settings, and managed yt-dlp binary. Ensure the data and media folders are writable by the configured user. Use `docker compose logs -f fetcharr` to view logs and `docker compose down` to stop; the host files remain.

   To use the published image without building locally:

   ```sh
   docker run -d --name fetcharr \
     --user "$(id -u):$(id -g)" \
     -p 8080:8080 \
     -v "$PWD/.local/fetcharr/config:/config" \
     -v "$PWD/.local/fetcharr/data:/data" \
     -v "$PWD/.local/fetcharr/media:/media" \
     --restart unless-stopped \
     ghcr.io/istarvin/fetcharr-server:latest
   ```

3. Check it: `curl http://localhost:8080/api/health`. The first start downloads yt-dlp into `/data/bin` and aborts if that download fails, so the container needs internet access on first start (or set `ytdlp.binary` to a binary you mount in).

4. Open `http://localhost:8080/` in a browser (sign in with the token if one is set), or connect Android using the server URL and optional token. Then open **Settings → Server settings**. Add categories using container paths such as `/media/movies`. Configure download limits, history retention, automatic yt-dlp updates, and Jellyfin/Plex connections there. Until a category is added, the API remains available but cannot accept downloads.

The image is Debian-based (glibc) so the standalone yt-dlp release binary runs without Python. It includes a `HEALTHCHECK` on `/api/health` at port 8080. If you change `server.port` in the config, keep it at 8080 inside the container and remap with `ports:` (or override the healthcheck).

Build locally from the repository root with `docker build -f server/Dockerfile -t fetcharr-server .` (optional `--build-arg VERSION=1.2.3`, reported by `GET /api/info`). The build context is the repository root so the web UI in `web/` can be compiled and embedded; the root `.dockerignore` keeps everything else (including `.local/`) out of it.

## Configuration

Start with [`config.minimal.yaml`](config.minimal.yaml). YAML owns listen host/port, authentication, log level, data directory, the optional custom yt-dlp executable, and arbitrary extra arguments. These deployment settings require a restart. The file is selected with `--config <path>`, `FETCHARR_CONFIG`, or defaults to `./config.yaml`; unknown keys are rejected.

Routine settings are saved in `<data_dir>/fetcharr.db` and apply live. Lowering concurrency lets current work finish. New download attempts use the latest download settings. History retention removes records only; files remain. Adding a category creates missing destination folders, including parents; destinations must be writable. Paths/layouts cannot change while queued, paused, downloading, or processing jobs reference them. Docker mounts and filesystem permissions are configured on the host.

Existing full YAML files remain supported: their routine settings are imported into SQLite once, on the first successful startup after upgrading. Afterward, SQLite is authoritative for those fields, so editing the old YAML does not reset changes made in Android. You can reduce the YAML to the deployment fields after import. Keep and back up the data directory; the database also stores integration credentials with the same private file permissions as job headers. Integration credentials can be replaced in Android but are never returned by the API. The config mount can be read-only.

The server validates deployment settings before opening the database and validates effective categories afterward. It refuses startup if effective category paths are missing or unwritable, `data_dir` is unwritable, or `ffmpeg`/`ffprobe` are missing. Zero categories is a valid setup state. Details: [spec section 3](../docs/fetcharr-server.md#3-configuration).

Set `server.auth_token` if the server is reachable by anyone you do not trust; clients then send `Authorization: Bearer <token>`, and the web UI asks for the token once and keeps an `HttpOnly` session cookie. `server.web_ui: false` turns the browser UI off. Behind a reverse proxy, forward the WebSocket and the original `Host` header ([spec §10](../docs/fetcharr-server.md#reverse-proxy)).

Server settings use the same authentication as job operations. There is no separate administrator role. Older Android versions keep working with the server; newer Android versions show an upgrade message when connected to a server without settings support.

## Bare metal

Requirements: Go (version in `go.mod`) and Node 24 to build, plus `ffmpeg` and `ffprobe` on `PATH` at runtime. Without the web build the server still works and serves a short notice at `/`.

```sh
(cd web && npm ci && npm run build)
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
