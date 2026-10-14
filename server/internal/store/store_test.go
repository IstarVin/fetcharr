package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/timefmt"
)

var ctx = context.Background()

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fetcharr.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func newJob(url string) *Job {
	return &Job{Category: "movies", URL: url, Title: Ptr("T " + url)}
}

func mustInsert(t *testing.T, s *Store, j *Job) *Job {
	t.Helper()
	if err := s.Insert(ctx, j); err != nil {
		t.Fatalf("insert %s: %v", j.URL, err)
	}
	return j
}

func TestOpenCreatesFileWith0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", st.Mode().Perm())
	}
	// Writing creates -wal/-shm; they must not be wider either.
	mustInsert(t, s, newJob("https://a"))
	for _, suf := range []string{"-wal", "-shm"} {
		if st, err := os.Stat(path + suf); err == nil && st.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s mode = %o", suf, st.Mode().Perm())
		}
	}
}

func TestOpenChmodsExistingWideFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", st.Mode().Perm())
	}
}

func TestPragmasAndMigrations(t *testing.T) {
	s, path := openTemp(t)
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q, %v", mode, err)
	}
	var fk, busy int
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if fk != 1 || busy != 5000 {
		t.Errorf("foreign_keys=%d busy_timeout=%d", fk, busy)
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil || v != LatestSchemaVersion() || v < 1 {
		t.Errorf("version = %d, %v (latest %d)", v, err, LatestSchemaVersion())
	}

	// Reopen: migrations are idempotent and data survives.
	mustInsert(t, s, newJob("https://persist"))
	_ = s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if v2, _ := s2.SchemaVersion(ctx); v2 != v {
		t.Errorf("version after reopen = %d", v2)
	}
	if _, total, _ := s2.List(ctx, ListFilter{}); total != 1 {
		t.Errorf("total after reopen = %d", total)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	s, path := openTemp(t)
	if _, err := s.db.ExecContext(ctx, "PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("err = %v", err)
	}
}

func TestLoadMigrations(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil || len(ms) == 0 {
		t.Fatalf("%v %d", err, len(ms))
	}
	for i, m := range ms {
		if m.version != i+1 {
			t.Errorf("migration %s has version %d, want contiguous %d", m.name, m.version, i+1)
		}
	}
}

// The partial unique index must cover exactly jobs.ActiveStatuses().
func TestActiveIndexMatchesActiveStatuses(t *testing.T) {
	s, _ := openTemp(t)
	var def string
	if err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name = 'jobs_active_url'`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "UNIQUE") {
		t.Errorf("index is not unique: %s", def)
	}
	for _, st := range jobs.AllStatuses() {
		has := strings.Contains(def, "'"+string(st)+"'")
		if has != jobs.IsActive(st) {
			t.Errorf("status %s: in index = %v, IsActive = %v", st, has, jobs.IsActive(st))
		}
	}
}

func TestInsertGetRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	created := time.Date(2026, 10, 14, 14, 30, 0, 1_000_000, time.UTC)
	started := created.Add(5 * time.Second)
	in := &Job{
		ID:        "0f6c1a6e-6f5e-4a8a-9a0e-2b7f8f4f1c11",
		BatchID:   Ptr("b1"),
		Category:  "tv",
		URL:       "https://cdn.example/s02e01.m3u8",
		Headers:   map[string]string{"Referer": "https://example.com/", "Cookie": "secret"},
		Title:     Ptr("Hello, Ms. Cobel"),
		ShowName:  Ptr("Severance"),
		Season:    Ptr(0),
		Episode:   Ptr(1),
		Year:      Ptr(2022),
		Subtitles: []Subtitle{{URL: "https://cdn.example/en.vtt", Lang: "en"}, {URL: "https://cdn.example/x.vtt"}},
		Status:    jobs.StatusDownloading,
		Progress:  &Progress{Percent: Ptr(42.7), DownloadedBytes: Ptr(int64(325058560)), TotalBytes: nil, SpeedBps: Ptr(int64(3250585)), ETASeconds: Ptr(int64(132))},
		Warnings:  []string{"subtitle es: failed"},
		Overwrite: true,
		Placing:   true,
		WorkDir:   "/data/work/0f6c",
		CreatedAt: created,
		StartedAt: &started,
	}
	mustInsert(t, s, in)
	out, err := s.Get(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.QueuedAt != created {
		t.Errorf("QueuedAt defaulted to %v, want %v", out.QueuedAt, created)
	}
	in.QueuedAt = created
	if *out.Progress.DownloadedBytes != 325058560 || *out.Progress.SpeedBps != 3250585 || *out.Progress.ETASeconds != 132 {
		t.Errorf("progress %+v", out.Progress)
	}
	if out.Progress.TotalBytes != nil || *out.Progress.Percent != 42.7 {
		t.Errorf("progress nulls lost: %+v", out.Progress)
	}
	if *out.BatchID != "b1" || *out.ShowName != "Severance" || *out.Season != 0 || *out.Episode != 1 || *out.Year != 2022 || *out.Title != "Hello, Ms. Cobel" {
		t.Errorf("metadata: %+v", out)
	}
	if out.Headers["Cookie"] != "secret" || len(out.Headers) != 2 {
		t.Errorf("headers: %v", out.Headers)
	}
	if len(out.Subtitles) != 2 || out.Subtitles[1].Lang != "" || out.Subtitles[0].Lang != "en" {
		t.Errorf("subtitles: %+v", out.Subtitles)
	}
	if !out.Overwrite || !out.Placing || out.WorkDir != "/data/work/0f6c" || out.Status != jobs.StatusDownloading {
		t.Errorf("flags: %+v", out)
	}
	if !out.CreatedAt.Equal(created) || out.StartedAt == nil || !out.StartedAt.Equal(started) || out.FinishedAt != nil {
		t.Errorf("times: %+v", out)
	}
	if out.FilePath != nil || out.ErrorCode != nil || out.ErrorMessage != nil {
		t.Errorf("unexpected non-null: %+v", out)
	}
	if len(out.Warnings) != 1 {
		t.Errorf("warnings: %v", out.Warnings)
	}
}

func TestInsertDefaultsAndNulls(t *testing.T) {
	s, _ := openTemp(t)
	j := mustInsert(t, s, &Job{Category: "misc", URL: "https://x", Title: Ptr("T")})
	if j.ID == "" || j.Status != jobs.StatusPending || j.CreatedAt.IsZero() {
		t.Fatalf("defaults not applied: %+v", j)
	}
	out, err := s.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.BatchID != nil || out.ShowName != nil || out.Season != nil || out.Episode != nil || out.Year != nil || out.Progress != nil ||
		out.StartedAt != nil || out.FinishedAt != nil {
		t.Errorf("expected nulls: %+v", out)
	}
	if out.Subtitles == nil || out.Warnings == nil || out.Headers == nil {
		t.Errorf("collections must be non-nil: %+v", out)
	}
	if out.QueuedAt != out.CreatedAt {
		t.Errorf("queued_at != created_at")
	}
	// tv job without title: title stays NULL
	tv := mustInsert(t, s, &Job{Category: "tv", URL: "https://tv", ShowName: Ptr("A"), Season: Ptr(1), Episode: Ptr(1)})
	if got, _ := s.Get(ctx, tv.ID); got.Title != nil {
		t.Errorf("title = %v", *got.Title)
	}
}

func TestStoredTimestampFormat(t *testing.T) {
	s, _ := openTemp(t)
	j := mustInsert(t, s, &Job{Category: "c", URL: "https://t", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)})
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT created_at FROM jobs WHERE id = ?`, j.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "2026-01-02T03:04:05.000Z" {
		t.Errorf("stored %q", raw)
	}
}

func TestInsertValidation(t *testing.T) {
	s, _ := openTemp(t)
	for name, j := range map[string]*Job{
		"nil":          nil,
		"no url":       {Category: "c"},
		"no category":  {URL: "https://x"},
		"unknown stat": {URL: "https://x", Category: "c", Status: "weird"},
	} {
		if err := s.Insert(ctx, j); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// duplicate primary key is not reported as ErrDuplicate
	a := mustInsert(t, s, newJob("https://a"))
	b := newJob("https://b")
	b.ID = a.ID
	if err := s.Insert(ctx, b); err == nil || errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate id: err = %v", err)
	}
}

func TestHeadersHaveOwnColumnAndNeverInJSON(t *testing.T) {
	s, _ := openTemp(t)
	j := newJob("https://h")
	j.Headers = map[string]string{"Authorization": "Bearer abc"}
	mustInsert(t, s, j)
	var h, other string
	if err := s.db.QueryRowContext(ctx, `SELECT headers FROM jobs WHERE id = ?`, j.ID).Scan(&h); err != nil || !strings.Contains(h, "Bearer abc") {
		t.Fatalf("headers column: %q %v", h, err)
	}
	// No other column contains the secret.
	cols := []string{"category", "url", "title", "subtitles", "warnings", "work_dir", "error_message"}
	for _, c := range cols {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(`+c+`, '') FROM jobs WHERE id = ?`, j.ID).Scan(&other); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(other, "Bearer") {
			t.Errorf("secret leaked into column %s", c)
		}
	}
	b, _ := jsonMarshal(j)
	if strings.Contains(string(b), "Bearer") || strings.Contains(strings.ToLower(string(b)), "headers") {
		t.Errorf("json tag safety net failed: %s", b)
	}
}

func TestDuplicateDetection(t *testing.T) {
	for _, active := range jobs.ActiveStatuses() {
		t.Run("blocked while "+string(active), func(t *testing.T) {
			s, _ := openTemp(t)
			first := newJob("https://dup")
			first.Status = active
			mustInsert(t, s, first)
			err := s.Insert(ctx, newJob("https://dup"))
			var d *DuplicateError
			if !errors.Is(err, ErrDuplicate) || !errors.As(err, &d) {
				t.Fatalf("err = %v", err)
			}
			if d.ExistingID != first.ID || d.URL != "https://dup" {
				t.Errorf("DuplicateError = %+v, want existing %s", d, first.ID)
			}
		})
	}
	for _, fin := range jobs.FinishedStatuses() {
		t.Run("allowed after "+string(fin), func(t *testing.T) {
			s, _ := openTemp(t)
			first := newJob("https://dup")
			first.Status = fin
			mustInsert(t, s, first)
			mustInsert(t, s, newJob("https://dup"))
			mustInsert(t, s, &Job{Category: "c", URL: "https://dup", Status: fin}) // many finished are fine
		})
	}
	t.Run("different urls fine", func(t *testing.T) {
		s, _ := openTemp(t)
		mustInsert(t, s, newJob("https://a"))
		mustInsert(t, s, newJob("https://b"))
	})
}

func TestConcurrentSameURLInsert(t *testing.T) {
	s, _ := openTemp(t)
	const n = 40
	var ok, dup, other atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := s.Insert(ctx, newJob("https://race"))
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrDuplicate):
				dup.Add(1)
			default:
				other.Add(1)
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if ok.Load() != 1 || dup.Load() != n-1 || other.Load() != 0 {
		t.Fatalf("ok=%d dup=%d other=%d", ok.Load(), dup.Load(), other.Load())
	}
	if _, total, _ := s.List(ctx, ListFilter{}); total != 1 {
		t.Errorf("rows = %d", total)
	}
}

func TestConcurrentBatchesSameURL(t *testing.T) {
	s, _ := openTemp(t)
	const n = 10
	var created atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.InsertBatch(ctx, []*Job{newJob("https://shared"), newJob(fmt.Sprintf("https://own-%d", i))})
			if err != nil {
				t.Errorf("batch: %v", err)
				return
			}
			for _, r := range res {
				if r == nil {
					created.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if created.Load() != n+1 {
		t.Errorf("created = %d, want %d", created.Load(), n+1)
	}
}

func TestInsertBatch(t *testing.T) {
	s, _ := openTemp(t)
	existing := mustInsert(t, s, newJob("https://e3"))
	bid := "batch-1"
	mk := func(url string, ep int) *Job {
		return &Job{BatchID: &bid, Category: "tv", URL: url, ShowName: Ptr("S"), Season: Ptr(1), Episode: Ptr(ep)}
	}
	batch := []*Job{mk("https://e1", 1), mk("https://e2", 2), mk("https://e3", 3), mk("https://e1", 4)}
	res, err := s.InsertBatch(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if res[0] != nil || res[1] != nil {
		t.Errorf("first two should insert: %v", res)
	}
	var d *DuplicateError
	if !errors.As(res[2], &d) || d.ExistingID != existing.ID {
		t.Errorf("res[2] = %v", res[2])
	}
	if !errors.Is(res[3], ErrDuplicate) { // duplicate inside the same batch
		t.Errorf("res[3] = %v", res[3])
	}
	got, err := s.ListByBatch(ctx, bid)
	if err != nil || len(got) != 2 {
		t.Fatalf("ListByBatch = %d, %v", len(got), err)
	}
	if *got[0].Episode != 1 || *got[1].Episode != 2 {
		t.Errorf("order: %d, %d", *got[0].Episode, *got[1].Episode)
	}
	if d := got[1].QueuedAt.Sub(got[0].QueuedAt); d != time.Millisecond {
		t.Errorf("queued_at spacing = %v, want 1ms", d)
	}
	if got[1].CreatedAt.Sub(got[0].CreatedAt) != time.Millisecond {
		t.Errorf("created_at spacing wrong")
	}
	// skipped ones left no rows
	if _, total, _ := s.List(ctx, ListFilter{BatchID: bid}); total != 2 {
		t.Errorf("rows in batch = %d", total)
	}
	// unknown batch -> empty
	if got, err := s.ListByBatch(ctx, "nope"); err != nil || len(got) != 0 {
		t.Errorf("unknown batch: %v %v", got, err)
	}
}

func TestInsertBatchAllDuplicatesAndInvalid(t *testing.T) {
	s, _ := openTemp(t)
	mustInsert(t, s, newJob("https://a"))
	res, err := s.InsertBatch(ctx, []*Job{newJob("https://a")})
	if err != nil || !errors.Is(res[0], ErrDuplicate) {
		t.Fatalf("%v %v", res, err)
	}
	// An invalid member aborts the whole batch and inserts nothing.
	_, err = s.InsertBatch(ctx, []*Job{newJob("https://ok"), {Category: "c"}})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err = %v", err)
	}
	if _, total, _ := s.List(ctx, ListFilter{}); total != 1 {
		t.Errorf("total = %d", total)
	}
	if res, err := s.InsertBatch(ctx, nil); err != nil || len(res) != 0 {
		t.Errorf("empty batch: %v %v", res, err)
	}
}

func TestInsertBatchRollsBackOnFatalError(t *testing.T) {
	s, _ := openTemp(t)
	a := newJob("https://a")
	a.ID = "same"
	b := newJob("https://b")
	b.ID = "same" // primary key clash -> fatal, not a "duplicate"
	if _, err := s.InsertBatch(ctx, []*Job{a, b}); err == nil {
		t.Fatal("expected error")
	}
	if _, total, _ := s.List(ctx, ListFilter{}); total != 0 {
		t.Errorf("rows after rollback = %d", total)
	}
}

func seedForList(t *testing.T, s *Store) map[string]*Job {
	t.Helper()
	base := time.Date(2026, 10, 14, 14, 0, 0, 0, time.UTC)
	mk := func(key, status, cat string, batch *string, created int, finished *int) *Job {
		j := &Job{ID: key, Category: cat, URL: "https://" + key, BatchID: batch, Status: jobs.Status(status), Title: Ptr(key),
			CreatedAt: base.Add(time.Duration(created) * time.Millisecond)}
		if finished != nil {
			f := base.Add(time.Duration(*finished) * time.Millisecond)
			j.FinishedAt = &f
		}
		return j
	}
	i := func(v int) *int { return &v }
	b1, b2 := "b1", "b2"
	all := map[string]*Job{}
	for _, j := range []*Job{
		mk("a", "pending", "tv", &b1, 1, nil),
		mk("b", "downloading", "tv", &b1, 2, nil),
		mk("c", "completed", "movies", nil, 3, i(100)),
		mk("d", "failed", "tv", &b2, 4, i(200)),
		mk("e", "cancelled", "movies", nil, 5, i(200)),
		mk("f", "paused", "misc", nil, 6, nil),
		mk("g", "completed", "tv", &b2, 7, i(300)),
	} {
		mustInsert(t, s, j)
		all[j.ID] = j
	}
	return all
}

func ids(js []*Job) string {
	var b strings.Builder
	for i, j := range js {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(j.ID)
	}
	return b.String()
}

func TestList(t *testing.T) {
	s, _ := openTemp(t)
	seedForList(t, s)
	base := time.Date(2026, 10, 14, 14, 0, 0, 0, time.UTC)
	after := func(ms int) *time.Time { t := base.Add(time.Duration(ms) * time.Millisecond); return &t }

	tests := []struct {
		name      string
		f         ListFilter
		want      string
		wantTotal int
	}{
		{"all newest first", ListFilter{}, "g,f,e,d,c,b,a", 7},
		{"single status", ListFilter{Statuses: []jobs.Status{jobs.StatusCompleted}}, "g,c", 2},
		{"status list", ListFilter{Statuses: []jobs.Status{jobs.StatusPending, jobs.StatusDownloading, jobs.StatusPaused}}, "f,b,a", 3},
		{"history statuses", ListFilter{Statuses: jobs.FinishedStatuses()}, "g,e,d,c", 4},
		{"category", ListFilter{Category: "tv"}, "g,d,b,a", 4},
		{"category + status", ListFilter{Category: "tv", Statuses: []jobs.Status{jobs.StatusCompleted}}, "g", 1},
		{"batch", ListFilter{BatchID: "b1"}, "b,a", 2},
		{"batch + status", ListFilter{BatchID: "b2", Statuses: []jobs.Status{jobs.StatusFailed}}, "d", 1},
		{"finished_after is strict", ListFilter{FinishedAfter: after(200)}, "g", 1},
		{"finished_after earlier", ListFilter{FinishedAfter: after(199)}, "g,e,d", 3},
		{"finished_after excludes unfinished", ListFilter{FinishedAfter: after(0)}, "g,e,d,c", 4},
		{"finished_after beyond", ListFilter{FinishedAfter: after(300)}, "", 0},
		{"limit", ListFilter{Limit: 2}, "g,f", 7},
		{"limit+offset", ListFilter{Limit: 2, Offset: 2}, "e,d", 7},
		{"offset only", ListFilter{Offset: 5}, "b,a", 7},
		{"offset past end", ListFilter{Limit: 5, Offset: 50}, "", 7},
		{"no match", ListFilter{Category: "nope"}, "", 0},
		{"everything combined", ListFilter{Statuses: jobs.FinishedStatuses(), Category: "tv", BatchID: "b2", FinishedAfter: after(250), Limit: 10}, "g", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, total, err := s.List(ctx, tc.f)
			if err != nil {
				t.Fatal(err)
			}
			if ids(got) != tc.want || total != tc.wantTotal {
				t.Errorf("got [%s] total %d; want [%s] total %d", ids(got), total, tc.want, tc.wantTotal)
			}
			if got == nil {
				t.Error("List must return a non-nil slice")
			}
		})
	}
	if _, _, err := s.List(ctx, ListFilter{Statuses: []jobs.Status{"bogus"}}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("bogus status: %v", err)
	}
}

func TestListSameCreatedAtNewestInsertFirst(t *testing.T) {
	s, _ := openTemp(t)
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"x1", "x2", "x3"} {
		mustInsert(t, s, &Job{ID: id, Category: "c", URL: "https://" + id, CreatedAt: ts})
	}
	got, _, _ := s.List(ctx, ListFilter{})
	if ids(got) != "x3,x2,x1" {
		t.Errorf("order = %s", ids(got))
	}
}

func TestPendingJobsOrder(t *testing.T) {
	s, _ := openTemp(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	mustInsert(t, s, &Job{ID: "late", Category: "c", URL: "https://late", CreatedAt: at(0), QueuedAt: at(50)})
	mustInsert(t, s, &Job{ID: "early", Category: "c", URL: "https://early", CreatedAt: at(10), QueuedAt: at(5)})
	mustInsert(t, s, &Job{ID: "tie1", Category: "c", URL: "https://tie1", CreatedAt: at(20), QueuedAt: at(30)})
	mustInsert(t, s, &Job{ID: "tie2", Category: "c", URL: "https://tie2", CreatedAt: at(21), QueuedAt: at(30)})
	mustInsert(t, s, &Job{ID: "dl", Category: "c", URL: "https://dl", Status: jobs.StatusDownloading, QueuedAt: at(1)})
	mustInsert(t, s, &Job{ID: "pa", Category: "c", URL: "https://pa", Status: jobs.StatusPaused, QueuedAt: at(2)})

	got, err := s.PendingJobs(ctx, 0)
	if err != nil || ids(got) != "early,tie1,tie2,late" {
		t.Fatalf("got %s, %v", ids(got), err)
	}
	got, _ = s.PendingJobs(ctx, 2)
	if ids(got) != "early,tie1" {
		t.Errorf("limit 2: %s", ids(got))
	}
	// Resuming a job moves it to the back of the queue by bumping queued_at.
	if _, err := s.Update(ctx, "early", Update{QueuedAt: Set(at(99))}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.PendingJobs(ctx, 0)
	if ids(got) != "tie1,tie2,late,early" {
		t.Errorf("after requeue: %s", ids(got))
	}
}

func TestUpdate(t *testing.T) {
	s, _ := openTemp(t)
	j := mustInsert(t, s, newJob("https://u"))
	now := timefmt.Now()

	got, err := s.Update(ctx, j.ID, Update{
		Status:    Set(jobs.StatusDownloading),
		Progress:  Set(&Progress{Percent: Ptr(10.5), SpeedBps: Ptr(int64(5))}),
		StartedAt: Set(&now),
		WorkDir:   Set("/w/1"),
		Warnings:  Set([]string{"w1"}),
		Placing:   Set(true),
		Overwrite: Set(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != jobs.StatusDownloading || *got.Progress.Percent != 10.5 || got.Progress.TotalBytes != nil ||
		!got.StartedAt.Equal(now) || got.WorkDir != "/w/1" || len(got.Warnings) != 1 || !got.Placing || !got.Overwrite {
		t.Errorf("updated: %+v", got)
	}
	if got.URL != j.URL || *got.Title != *j.Title {
		t.Errorf("untouched fields changed")
	}

	// Finish with an error, then clear fields (retry).
	fin := now.Add(time.Second)
	got, err = s.Update(ctx, j.ID, Update{
		Status: Set(jobs.StatusFailed), ErrorCode: Set(Ptr("download_failed")), ErrorMessage: Set(Ptr("ERROR: boom")),
		FinishedAt: Set(&fin), FilePath: Set(Ptr("/media/x.mp4")),
	})
	if err != nil || *got.ErrorCode != "download_failed" || *got.ErrorMessage != "ERROR: boom" || got.FinishedAt == nil || *got.FilePath != "/media/x.mp4" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = s.Update(ctx, j.ID, Update{
		Status: Set(jobs.StatusPending), ErrorCode: Set[*string](nil), ErrorMessage: Set[*string](nil), FilePath: Set[*string](nil),
		Progress: Set[*Progress](nil), Warnings: Set[[]string](nil), FinishedAt: Set[*time.Time](nil), StartedAt: Set[*time.Time](nil),
		Placing: Set(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ErrorCode != nil || got.ErrorMessage != nil || got.FilePath != nil || got.Progress != nil || got.FinishedAt != nil || got.StartedAt != nil ||
		got.Warnings == nil || len(got.Warnings) != 0 || got.Placing || got.Status != jobs.StatusPending {
		t.Errorf("cleared: %+v", got)
	}

	// Empty update is a read.
	if got, err := s.Update(ctx, j.ID, Update{}); err != nil || got.ID != j.ID {
		t.Errorf("empty update: %v %v", got, err)
	}
	if _, err := s.Update(ctx, "missing", Update{Status: Set(jobs.StatusPaused)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if _, err := s.Update(ctx, "missing", Update{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing empty: %v", err)
	}
	if _, err := s.Update(ctx, j.ID, Update{Status: Set(jobs.Status("weird"))}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("bad status: %v", err)
	}
}

func TestUpdateIf(t *testing.T) {
	s, _ := openTemp(t)
	j := mustInsert(t, s, newJob("https://g"))
	if _, err := s.UpdateIf(ctx, j.ID, []jobs.Status{jobs.StatusDownloading}, Update{Progress: Set(&Progress{Percent: Ptr(1.0)})}); !errors.Is(err, ErrStatusMismatch) {
		t.Errorf("mismatch: %v", err)
	}
	if got, _ := s.Get(ctx, j.ID); got.Progress != nil {
		t.Error("guarded update wrote anyway")
	}
	got, err := s.UpdateIf(ctx, j.ID, []jobs.Status{jobs.StatusPending, jobs.StatusPaused}, Update{Status: Set(jobs.StatusDownloading)})
	if err != nil || got.Status != jobs.StatusDownloading {
		t.Errorf("match: %v %v", got, err)
	}
	if _, err := s.UpdateIf(ctx, "nope", []jobs.Status{jobs.StatusPending}, Update{Placing: Set(true)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if _, err := s.UpdateIf(ctx, j.ID, nil, Update{}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("no statuses: %v", err)
	}
}

func TestUpdateConcurrentCASOnlyOneWins(t *testing.T) {
	s, _ := openTemp(t)
	j := mustInsert(t, s, newJob("https://cas"))
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.UpdateIf(ctx, j.ID, []jobs.Status{jobs.StatusPending}, Update{Status: Set(jobs.StatusDownloading)})
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrStatusMismatch) {
				t.Errorf("err = %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Errorf("wins = %d", wins.Load())
	}
}

// Retrying a finished job whose URL has since been queued again must hit the
// unique index instead of creating two active jobs.
func TestUpdateToActiveHitsDuplicateIndex(t *testing.T) {
	s, _ := openTemp(t)
	old := newJob("https://again")
	old.Status = jobs.StatusFailed
	mustInsert(t, s, old)
	cur := mustInsert(t, s, newJob("https://again"))

	_, err := s.Update(ctx, old.ID, Update{Status: Set(jobs.StatusPending)})
	var d *DuplicateError
	if !errors.As(err, &d) || d.ExistingID != cur.ID {
		t.Fatalf("err = %v", err)
	}
	if got, _ := s.Get(ctx, old.ID); got.Status != jobs.StatusFailed {
		t.Errorf("status changed despite error: %s", got.Status)
	}
	// Once the blocker is finished, the retry works.
	if _, err := s.Update(ctx, cur.ID, Update{Status: Set(jobs.StatusCancelled)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, old.ID, Update{Status: Set(jobs.StatusPending)}); err != nil {
		t.Errorf("retry after blocker finished: %v", err)
	}
}

func TestDelete(t *testing.T) {
	s, _ := openTemp(t)
	j := mustInsert(t, s, newJob("https://d"))
	if err := s.Delete(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, j.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete: %v", err)
	}
	if err := s.Delete(ctx, j.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
	if _, err := s.Get(ctx, "never"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get missing: %v", err)
	}
}

func TestDeleteMany(t *testing.T) {
	base := time.Date(2026, 10, 14, 14, 0, 0, 0, time.UTC)
	ms := func(n int) *time.Time { t := base.Add(time.Duration(n) * time.Millisecond); return &t }

	tests := []struct {
		name     string
		statuses []jobs.Status
		before   *time.Time
		want     string // sorted ids deleted
		remain   int
	}{
		{"default finished", nil, nil, "c,d,e,g", 3},
		{"only failed", []jobs.Status{jobs.StatusFailed}, nil, "d", 6},
		{"completed+cancelled", []jobs.Status{jobs.StatusCompleted, jobs.StatusCancelled}, nil, "c,e,g", 4},
		{"older than 250ms", nil, ms(250), "c,d,e", 4},
		{"older than 100ms (strict)", nil, ms(100), "", 7},
		{"failed older than 1s", []jobs.Status{jobs.StatusFailed}, ms(1000), "d", 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openTemp(t)
			seedForList(t, s)
			got, err := s.DeleteMany(ctx, tc.statuses, tc.before)
			if err != nil {
				t.Fatal(err)
			}
			if got == nil {
				t.Error("must return non-nil slice")
			}
			sortStrings(got)
			if strings.Join(got, ",") != tc.want {
				t.Errorf("deleted %v, want %s", got, tc.want)
			}
			if _, total, _ := s.List(ctx, ListFilter{}); total != tc.remain {
				t.Errorf("remaining = %d, want %d", total, tc.remain)
			}
		})
	}
	t.Run("active statuses rejected and nothing deleted", func(t *testing.T) {
		s, _ := openTemp(t)
		seedForList(t, s)
		for _, active := range jobs.ActiveStatuses() {
			if _, err := s.DeleteMany(ctx, []jobs.Status{jobs.StatusCompleted, active}, nil); !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("%s: err = %v", active, err)
			}
		}
		if _, total, _ := s.List(ctx, ListFilter{}); total != 7 {
			t.Errorf("total = %d", total)
		}
	})
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestRequeueInterrupted(t *testing.T) {
	s, _ := openTemp(t)
	mk := func(id string, st jobs.Status) {
		mustInsert(t, s, &Job{ID: id, Category: "c", URL: "https://" + id, Status: st, WorkDir: "/w/" + id, Placing: st == jobs.StatusProcessing})
	}
	mk("dl", jobs.StatusDownloading)
	mk("pr", jobs.StatusProcessing)
	mk("pa", jobs.StatusPaused)
	mk("pe", jobs.StatusPending)
	mk("ok", jobs.StatusCompleted)
	got, err := s.RequeueInterrupted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sortJobs := ids(got)
	if sortJobs != "dl,pr" && sortJobs != "pr,dl" {
		t.Errorf("requeued %s", sortJobs)
	}
	for _, id := range []string{"dl", "pr", "pe"} {
		j, _ := s.Get(ctx, id)
		if j.Status != jobs.StatusPending || j.Placing {
			t.Errorf("%s: %s placing=%v", id, j.Status, j.Placing)
		}
	}
	if j, _ := s.Get(ctx, "dl"); j.WorkDir != "/w/dl" {
		t.Error("work dir must be kept")
	}
	if j, _ := s.Get(ctx, "pa"); j.Status != jobs.StatusPaused {
		t.Error("paused must stay paused")
	}
	if j, _ := s.Get(ctx, "ok"); j.Status != jobs.StatusCompleted {
		t.Error("completed untouched")
	}
	if again, _ := s.RequeueInterrupted(ctx); len(again) != 0 {
		t.Errorf("second run = %d", len(again))
	}
}

func TestContextCancelled(t *testing.T) {
	s, _ := openTemp(t)
	c, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Insert(c, newJob("https://x")); err == nil {
		t.Error("expected error with cancelled context")
	}
	// Store still works afterwards.
	mustInsert(t, s, newJob("https://y"))
}

func TestSQLiteDriverRegistered(t *testing.T) {
	found := false
	for _, n := range sql.Drivers() {
		if n == "sqlite" {
			found = true
		}
	}
	if !found {
		t.Error("sqlite driver not registered")
	}
}

func TestJobLogValueHidesHeaders(t *testing.T) {
	j := &Job{ID: "i", URL: "https://x?token=1", Headers: map[string]string{"Cookie": "secret"}, Category: "c", Status: jobs.StatusPending}
	s := j.LogValue().String()
	if strings.Contains(s, "secret") || strings.Contains(s, "token=1") {
		t.Errorf("leaks: %s", s)
	}
	var nilJob *Job
	_ = nilJob.LogValue()
}
