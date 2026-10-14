// Package store persists jobs in SQLite (modernc.org/sqlite, no CGO).
//
// The database file is created with mode 0600 because it holds request
// headers (spec §9). Schema changes are embedded, versioned migrations
// tracked with PRAGMA user_version.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Errors returned by the store.
var (
	// ErrNotFound: no job with that id.
	ErrNotFound = errors.New("job not found")
	// ErrDuplicate: an active job with the same URL exists. Returned errors
	// are *DuplicateError, which wraps this sentinel.
	ErrDuplicate = errors.New("duplicate: an active job with this url exists")
	// ErrStatusMismatch: a guarded update found the job in another status.
	ErrStatusMismatch = errors.New("job is not in the expected status")
	// ErrInvalidArgument: the caller passed an unusable value.
	ErrInvalidArgument = errors.New("invalid argument")
)

// DuplicateError reports a URL that is already active. ExistingID is the id
// of the blocking job when it could be looked up ("" otherwise).
type DuplicateError struct {
	URL        string
	ExistingID string
}

func (e *DuplicateError) Error() string {
	if e.ExistingID != "" {
		return fmt.Sprintf("%v (job %s)", ErrDuplicate, e.ExistingID)
	}
	return ErrDuplicate.Error()
}

// Is makes errors.Is(err, ErrDuplicate) work.
func (e *DuplicateError) Is(target error) bool { return target == ErrDuplicate }

// Store is a SQLite-backed job store. It is safe for concurrent use.
type Store struct {
	db *sql.DB
}

// Open creates (mode 0600) or opens the database at path, enables WAL,
// busy_timeout and foreign keys, and applies pending migrations. An existing
// file with group/other permissions is chmod'ed to 0600.
func Open(path string) (*Store, error) {
	if err := ensureFile(path); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	dsn := (&url.URL{Scheme: "file", Opaque: escapePath(path), RawQuery: q.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	s := &Store{db: db}
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	// The -wal / -shm files inherit the main file's mode, but be explicit.
	for _, suffix := range []string{"-wal", "-shm"} {
		if st, err := os.Stat(path + suffix); err == nil && st.Mode().Perm()&0o077 != 0 {
			_ = os.Chmod(path+suffix, 0o600)
		}
	}
	return s, nil
}

// escapePath escapes characters that are special in a SQLite file: URI.
func escapePath(p string) string {
	r := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")
	return r.Replace(p)
}

func ensureFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("create database file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("create database file: %w", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Mode().Perm() != 0o600 {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("chmod database file: %w", err)
		}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// SchemaVersion returns the applied schema version (PRAGMA user_version).
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

// LatestSchemaVersion is the highest embedded migration number.
func LatestSchemaVersion() int {
	ms, _ := loadMigrations()
	if len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].version
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		numStr, _, _ := strings.Cut(name, "_")
		v, err := strconv.Atoi(numStr)
		if err != nil {
			return nil, fmt.Errorf("migration %s: name must start with a number", name)
		}
		b, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: v, name: name, sql: string(b)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i := 1; i < len(ms); i++ {
		if ms[i].version == ms[i-1].version {
			return nil, fmt.Errorf("duplicate migration version %d", ms[i].version)
		}
	}
	return ms, nil
}

func (s *Store) migrate(ctx context.Context) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	return s.withWriteTx(ctx, func(c *sql.Conn) error {
		var cur int
		if err := c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&cur); err != nil {
			return err
		}
		if len(ms) > 0 && cur > ms[len(ms)-1].version {
			return fmt.Errorf("database schema version %d is newer than this server supports (%d)", cur, ms[len(ms)-1].version)
		}
		for _, m := range ms {
			if m.version <= cur {
				continue
			}
			if _, err := c.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("apply migration %s: %w", m.name, err)
			}
			// PRAGMA does not accept bound parameters.
			if _, err := c.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(m.version)); err != nil {
				return fmt.Errorf("set schema version %d: %w", m.version, err)
			}
		}
		return nil
	})
}

// withWriteTx runs fn inside BEGIN IMMEDIATE ... COMMIT on one connection, so
// concurrent writers queue on busy_timeout instead of failing on lock upgrade.
func (s *Store) withWriteTx(ctx context.Context, fn func(c *sql.Conn) error) (err error) {
	c, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if _, err := c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			// Use a fresh context: ctx may be the reason we are here.
			_, _ = c.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
	}()
	if err := fn(c); err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}
