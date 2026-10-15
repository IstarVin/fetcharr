CREATE TABLE server_settings (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    revision INTEGER NOT NULL,
    body TEXT NOT NULL
);
ALTER TABLE jobs ADD COLUMN destination_root TEXT NOT NULL DEFAULT '';
