package settings_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/naming"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/settings"
	"github.com/istarvin/fetcharr/server/internal/store"
)

func fixture(t *testing.T) (*config.Config, *store.Store, *settings.Manager) {
	t.Helper()
	c := config.Default()
	c.DataDir = t.TempDir()
	c.Categories = []config.Category{{ID: "misc", Label: "Misc", Path: t.TempDir(), Layout: naming.LayoutFlat}}
	c.Downloads.ExtraArgs = []string{"--limit-rate", "5M"}
	c.MediaServers = []config.MediaServer{{Type: "jellyfin", URL: "http://jellyfin:8096", APIKey: "private-key"}}
	s, err := store.Open(c.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	m, err := settings.Open(t.Context(), c, s)
	if err != nil {
		t.Fatal(err)
	}
	return c, s, m
}

func TestImportPersistenceAndWriteOnlyCredentials(t *testing.T) {
	c, s, m := fixture(t)
	d := m.Get()
	if d.Revision != 1 || len(d.MediaServers) != 1 || !d.MediaServers[0].HasCredential || d.MediaServers[0].Credential != nil {
		t.Fatalf("bad imported settings")
	}
	body, _ := json.Marshal(d)
	if strings.Contains(string(body), "private-key") || strings.Contains(string(body), "extra_args") || strings.Contains(string(body), "auth_token") {
		t.Fatal("public settings leaked private configuration")
	}
	d.Downloads.Concurrency = 5
	if _, err := m.Save(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if c.Current().Downloads.Concurrency != 5 || c.Current().MediaServers[0].APIKey != "private-key" || len(c.Current().Downloads.ExtraArgs) != 2 {
		t.Fatal("live configuration was not preserved/applied")
	}
	if _, err := m.Save(t.Context(), d); !errors.Is(err, settings.ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	// YAML is now only authoritative for deployment fields. Even obsolete,
	// invalid mutable YAML values must not displace the database settings.
	restarted := c.Clone()
	restarted.Categories = []config.Category{{ID: "obsolete", Path: "/missing", Layout: "invalid"}}
	restarted.Downloads.Concurrency = -1
	restarted.MediaServers = nil
	m2, err := settings.Open(t.Context(), restarted, s)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Get().Revision != 2 || restarted.Current().Downloads.Concurrency != 5 || restarted.Current().MediaServers[0].APIKey != "private-key" {
		t.Fatal("saved settings lost on restart")
	}
	// Editing a public result cannot mutate retained state.
	public := m2.Get()
	public.Categories[0].Path = "/tampered"
	if m2.Get().Categories[0].Path == "/tampered" {
		t.Fatal("mutable public result")
	}
	replacement := "replacement"
	d = m2.Get()
	d.MediaServers[0].Credential = &replacement
	if _, err := m2.Save(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if restarted.Current().MediaServers[0].APIKey != replacement {
		t.Fatal("credential was not replaced")
	}
	d = m2.Get()
	d.MediaServers = []settings.Integration{}
	if _, err := m2.Save(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	_, stored, _ := s.ReadSettings(t.Context())
	if strings.Contains(string(stored), replacement) || len(restarted.Current().MediaServers) != 0 {
		t.Fatal("removed credential retained")
	}
}

func TestInvalidAndFailedSavesLeaveConfigurationUnchanged(t *testing.T) {
	c, s, m := fixture(t)
	d := m.Get()
	d.Categories[0].Path = filepath.Join(t.TempDir(), "missing")
	if _, err := m.Save(t.Context(), d); err == nil {
		t.Fatal("missing path accepted")
	}
	if _, err := os.Stat(d.Categories[0].Path); !os.IsNotExist(err) {
		t.Fatal("save created a category directory")
	}
	d = m.Get()
	d.MediaServers = append(d.MediaServers, settings.Integration{ID: "new", Type: "plex", URL: "http://plex:32400"})
	if _, err := m.Save(t.Context(), d); err == nil {
		t.Fatal("missing credential accepted")
	}
	if m.Get().Revision != 1 {
		t.Fatal("validation changed revision")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	d = m.Get()
	d.Downloads.Concurrency = 9
	if _, err := m.Save(t.Context(), d); err == nil {
		t.Fatal("save to closed database succeeded")
	}
	if m.Get().Revision != 1 || c.Current().Downloads.Concurrency != 2 {
		t.Fatal("failed persistence changed live settings")
	}
}

func TestActiveCategoriesProtectedAndHistoricalFilesKeepTheirRoot(t *testing.T) {
	c, s, m := fixture(t)
	svc := service.New(s, c)
	j, err := svc.Create(t.Context(), service.CreateRequest{URL: "https://example.com/video", Category: "misc", Title: "Movie"})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range jobs.ActiveStatuses() {
		if _, err := s.Update(t.Context(), j.ID, store.Update{Status: store.Set(status)}); err != nil {
			t.Fatal(err)
		}
		for _, change := range []string{"remove", "path", "layout"} {
			d := m.Get()
			switch change {
			case "remove":
				d.Categories = []config.Category{}
			case "path":
				d.Categories[0].Path = t.TempDir()
			case "layout":
				d.Categories[0].Layout = naming.LayoutTV
			}
			if _, err := m.Save(t.Context(), d); !errors.Is(err, settings.ErrCategoryBusy) {
				t.Fatalf("%s/%s: %v", status, change, err)
			}
		}
	}
	file := filepath.Join(c.Categories[0].Path, "Movie.mp4")
	if err := os.WriteFile(file, []byte("movie"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(t.Context(), j.ID, store.Update{Status: store.Set(jobs.StatusCompleted), FilePath: store.Set(&file)}); err != nil {
		t.Fatal(err)
	}
	d := m.Get()
	d.Categories = []config.Category{}
	if _, err := m.Save(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(t.Context(), j.ID, service.DeleteOptions{DeleteFile: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("historical file was not removed from its original root")
	}
	if _, err := svc.Create(t.Context(), service.CreateRequest{Category: "misc", URL: "https://example.com/new"}); err == nil {
		t.Fatal("submission accepted without a category")
	}
}

func TestCategoryRemovalAndSubmissionAreCoordinated(t *testing.T) {
	for range 20 {
		c, s, m := fixture(t)
		svc := service.New(s, c)
		d := m.Get()
		d.Categories = []config.Category{}
		start := make(chan struct{})
		var createErr, saveErr error
		var wg sync.WaitGroup
		wg.Go(func() {
			<-start
			_, createErr = svc.Create(t.Context(), service.CreateRequest{Category: "misc", URL: "https://example.com/race", Title: "Race"})
		})
		wg.Go(func() { <-start; _, saveErr = m.Save(t.Context(), d) })
		close(start)
		wg.Wait()
		if createErr == nil && saveErr == nil {
			t.Fatal("category removed while an accepted job references it")
		}
		if createErr != nil && saveErr != nil {
			t.Fatalf("neither operation succeeded: %v / %v", createErr, saveErr)
		}
	}
}

func TestLegacyRootBackfillAndRetryValidation(t *testing.T) {
	c := config.Default()
	c.DataDir = t.TempDir()
	c.Categories = []config.Category{{ID: "misc", Path: t.TempDir(), Layout: naming.LayoutFlat}}
	s, err := store.Open(c.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	legacy := &store.Job{Category: "misc", URL: "https://example.com/legacy", Title: store.Ptr("Legacy"), Status: jobs.StatusFailed}
	unknown := &store.Job{Category: "old", URL: "https://example.com/unknown", Status: jobs.StatusCompleted, FilePath: store.Ptr("/unknown/file.mp4")}
	for _, j := range []*store.Job{legacy, unknown} {
		if err := s.Insert(t.Context(), j); err != nil {
			t.Fatal(err)
		}
	}
	m, err := settings.Open(t.Context(), c, s)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(t.Context(), legacy.ID)
	if got.DestinationRoot != c.Categories[0].Path {
		t.Fatal("legacy root not backfilled")
	}
	svc := service.New(s, c)
	if err := svc.Delete(t.Context(), unknown.ID, service.DeleteOptions{DeleteFile: true}); err == nil {
		t.Fatal("file deletion guessed an unknown root")
	}
	d := m.Get()
	d.Categories[0].Layout = naming.LayoutTV
	if _, err := m.Save(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Retry(t.Context(), legacy.ID, service.RetryOptions{}); err == nil {
		t.Fatal("retry accepted metadata incompatible with new layout")
	}
	d = m.Get()
	d.Categories[0].Layout = naming.LayoutFlat
	d.Categories[0].Path = t.TempDir()
	if _, err := m.Save(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	got, err = svc.Retry(t.Context(), legacy.ID, service.RetryOptions{})
	if err != nil || got.DestinationRoot != d.Categories[0].Path {
		t.Fatalf("retry did not refresh destination: %v", err)
	}
}
