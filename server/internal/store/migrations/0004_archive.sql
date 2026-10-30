-- Season-pack jobs: the archive spec (NULL for ordinary jobs) and the
-- library files an archive job placed.
ALTER TABLE jobs ADD COLUMN archive TEXT;
ALTER TABLE jobs ADD COLUMN file_paths TEXT NOT NULL DEFAULT '[]';
