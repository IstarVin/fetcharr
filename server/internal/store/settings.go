package store

import (
	"context"
	"database/sql"
	"errors"
)

// ReadSettings returns ErrNotFound until the initial YAML import.
func (s *Store) ReadSettings(ctx context.Context) (int64, []byte, error) {
	var revision int64
	var body string
	err := s.db.QueryRowContext(ctx, `SELECT revision, body FROM server_settings WHERE singleton = 1`).Scan(&revision, &body)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return revision, []byte(body), err
}

// SaveSettings uses a database compare-and-set as a second line of protection
// against stale revisions. A failed write never changes in-memory settings.
func (s *Store) SaveSettings(ctx context.Context, revision int64, body []byte) error {
	var result sql.Result
	var err error
	if revision == 0 {
		result, err = s.db.ExecContext(ctx, `INSERT INTO server_settings(singleton, revision, body) VALUES(1, 1, ?) ON CONFLICT DO NOTHING`, string(body))
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE server_settings SET revision = revision + 1, body = ? WHERE singleton = 1 AND revision = ?`, string(body), revision)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStatusMismatch
	}
	return nil
}

// BackfillDestinationRoots only assigns previously unknown roots; subsequent
// category edits must never rewrite the origin of historical files.
func (s *Store) BackfillDestinationRoots(ctx context.Context, category, root string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET destination_root = ? WHERE category = ? AND destination_root = ''`, root, category)
	return err
}
