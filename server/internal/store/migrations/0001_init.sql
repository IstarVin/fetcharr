-- Initial schema. Timestamps are TEXT in RFC 3339 UTC with millisecond
-- precision (timefmt), so they sort lexicographically.
CREATE TABLE jobs (
    id            TEXT PRIMARY KEY,
    batch_id      TEXT,
    category      TEXT NOT NULL,
    url           TEXT NOT NULL,
    -- Request headers (JSON object). Own column; never part of any API DTO.
    headers       TEXT NOT NULL DEFAULT '{}',
    title         TEXT,
    show_name     TEXT,
    season        INTEGER,
    episode       INTEGER,
    year          INTEGER,
    subtitles     TEXT NOT NULL DEFAULT '[]',   -- JSON [{url, lang}]
    status        TEXT NOT NULL CHECK (status IN
                    ('pending','downloading','processing','paused','completed','failed','cancelled')),
    progress      TEXT,                         -- JSON object or NULL
    file_path     TEXT,
    error_code    TEXT,
    error_message TEXT,
    warnings      TEXT NOT NULL DEFAULT '[]',   -- JSON [string]
    overwrite     INTEGER NOT NULL DEFAULT 0,
    placing       INTEGER NOT NULL DEFAULT 0,
    work_dir      TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    queued_at     TEXT NOT NULL,
    started_at    TEXT,
    finished_at   TEXT
);

-- Duplicate detection (spec §6.1): at most one active job per URL. The status
-- list must equal jobs.ActiveStatuses() (checked by a test).
CREATE UNIQUE INDEX jobs_active_url ON jobs(url)
    WHERE status IN ('pending','downloading','processing','paused');

CREATE INDEX jobs_created_at  ON jobs(created_at DESC);
CREATE INDEX jobs_batch       ON jobs(batch_id) WHERE batch_id IS NOT NULL;
CREATE INDEX jobs_queue       ON jobs(queued_at) WHERE status = 'pending';
CREATE INDEX jobs_finished_at ON jobs(finished_at) WHERE finished_at IS NOT NULL;
