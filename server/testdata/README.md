# testdata

## fake-ytdlp

A python3 stand-in for `yt-dlp`, used by server tests (set `ytdlp.binary` to it,
i.e. unmanaged mode). Accepts the arguments the server passes per
`docs/fetcharr-server.md` section 4.2 and ignores options it does not know.

Behaviour:

- `--version` prints `2099.01.01`; `-U` prints an "up to date" message. Both exit 0.
- `-o <dir>/media.%(ext)s` (or `-P <dir>` plus `-o`): writes a 4 MiB dummy
  `media.<ext>` (`--merge-output-format` value, default `mp4`) after streaming it through
  `media.<ext>.part`.
- `--progress-template`: one line per tick (20 ticks). Supports `%(progress.<key>)s|d|j`
  and `%(progress)j`; a leading `download:` is stripped. Fields: `status`, `downloaded_bytes`,
  `total_bytes`, `total_bytes_estimate` (always `None`/`null`), `speed`, `eta`, `elapsed`, `filename`.
  A final `status: finished` line follows. Without a template, plain `[download] NN%` lines are printed.
  Non-JSON noise lines (`[generic] ...`, `[download] Destination: ...`) are also emitted on stdout,
  so parsers must skip lines that are not JSON.
- `--print after_move:filepath` (any `--print` ending in `filepath`): final path printed as the last stdout line.
- `FAKE_YTDLP_DURATION` (seconds, default `4`; `0` = instant) controls total runtime.
- Resume: if `media.<ext>.part` exists, progress starts from its size and only the rest is written.
- SIGTERM/SIGINT: exits immediately with code 143, leaving the `.part` file.
- Failure: if the URL (last positional, normally after `--`) contains `fail`, prints
  `ERROR: [generic] simulated failure` to stderr and exits 1.
- Transient failure: if the URL contains `flaky`, the first run in a work directory fails the
  same way (leaving a `flaky-attempted` marker there) and later runs succeed.
