// Package settings owns the server's persistent, live-editable configuration.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/store"
)

var (
	// ErrConflict rejects saves based on an outdated revision.
	ErrConflict = errors.New("settings changed; reload before saving")
	// ErrCategoryBusy prevents changing a destination used by active jobs.
	ErrCategoryBusy = errors.New("category has active jobs")
)

// ValidationError describes an invalid settings document.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// Downloads contains limits and output preferences for new attempts.
type Downloads struct {
	Concurrency  int    `json:"concurrency"`
	MergeFormat  string `json:"merge_format"`
	MinFreeSpace int64  `json:"min_free_space"` // bytes
}

// History configures retention of finished job records.
type History struct {
	RetentionDays int `json:"retention_days"`
}

// Ytdlp configures automatic updates of the managed executable.
type Ytdlp struct {
	AutoUpdate bool `json:"auto_update"`
}

func absoluteRemotePath(path string) bool {
	if filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) {
		return true
	}
	return len(path) >= 3 && ((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z')) && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}

// Integration describes a media server connection. Credential is write-only
// over HTTP; the database stores it in its private document. HasCredential
// lets an editor leave an existing secret unchanged.
type Integration struct {
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	URL           string            `json:"url"`
	PathMap       map[string]string `json:"path_map"`
	Credential    *string           `json:"credential,omitempty"`
	HasCredential bool              `json:"has_credential"`
}

// Document deliberately cannot represent deployment settings or extra args.
type Document struct {
	Revision     int64             `json:"revision"`
	Downloads    Downloads         `json:"downloads"`
	History      History           `json:"history"`
	Ytdlp        Ytdlp             `json:"ytdlp"`
	Categories   []config.Category `json:"categories"`
	MediaServers []Integration     `json:"media_servers"`
}

// Manager validates, persists and publishes live settings atomically.
type Manager struct {
	mu      sync.Mutex
	cfg     *config.Config
	runtime *config.Runtime
	store   *store.Store
	doc     Document
	// Changed is a nonblocking startup-wired hook (e.g. wake the runner).
	Changed func()
}

func fromConfig(c *config.Config) Document {
	d := Document{
		Downloads: Downloads{c.Downloads.Concurrency, c.Downloads.MergeFormat, int64(c.Downloads.MinFreeSpace)},
		History:   History{c.History.RetentionDays}, Ytdlp: Ytdlp{c.Ytdlp.AutoUpdate},
		Categories: append([]config.Category{}, c.Categories...), MediaServers: []Integration{},
	}
	for _, s := range c.MediaServers {
		credential := s.APIKey
		if s.Type == config.MediaServerPlex {
			credential = s.Token
		}
		paths := s.PathMap
		if paths == nil {
			paths = map[string]string{}
		}
		d.MediaServers = append(d.MediaServers, Integration{ID: store.NewID(), Type: s.Type, URL: s.URL, PathMap: paths, Credential: &credential, HasCredential: credential != ""})
	}
	return d
}

func effective(base *config.Config, d Document) *config.Config {
	c := base.Clone()
	c.Downloads.Concurrency, c.Downloads.MergeFormat = d.Downloads.Concurrency, d.Downloads.MergeFormat
	c.Downloads.MinFreeSpace = config.ByteSize(d.Downloads.MinFreeSpace)
	c.History.RetentionDays, c.Ytdlp.AutoUpdate = d.History.RetentionDays, d.Ytdlp.AutoUpdate
	c.Categories = d.Categories
	c.MediaServers = nil
	for _, s := range d.MediaServers {
		ms := config.MediaServer{Type: s.Type, URL: s.URL, PathMap: s.PathMap}
		if s.Credential != nil {
			if s.Type == config.MediaServerPlex {
				ms.Token = *s.Credential
			} else {
				ms.APIKey = *s.Credential
			}
		}
		c.MediaServers = append(c.MediaServers, ms)
	}
	return c
}

// Open imports legacy YAML exactly once. It must run before goroutines start.
func Open(ctx context.Context, cfg *config.Config, st *store.Store) (*Manager, error) {
	revision, body, err := st.ReadSettings(ctx)
	var d Document
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	importing := errors.Is(err, store.ErrNotFound)
	if importing {
		d = fromConfig(cfg)
	} else {
		if err := json.Unmarshal(body, &d); err != nil {
			return nil, fmt.Errorf("read saved settings: %w", err)
		}
	}
	c := effective(cfg, d)
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := c.ValidateDestinations(); err != nil {
		return nil, err
	}
	if importing {
		d.Revision = 1
		body, err = json.Marshal(d)
		if err != nil {
			return nil, err
		}
		if err := st.SaveSettings(ctx, 0, body); err != nil {
			return nil, err
		}
		revision = 1
	}
	d.Revision = revision
	if importing {
		for _, cat := range c.Categories {
			if err := st.BackfillDestinationRoots(ctx, cat.ID, cat.Path); err != nil {
				return nil, err
			}
		}
	}
	r := cfg.EnableRuntime()
	r.Publish(c)
	return &Manager{cfg: cfg, runtime: r, store: st, doc: cloneDocument(d)}, nil
}

func cloneDocument(d Document) Document {
	d.Categories = slices.Clone(d.Categories)
	d.MediaServers = slices.Clone(d.MediaServers)
	for i := range d.MediaServers {
		s := &d.MediaServers[i]
		s.PathMap = maps.Clone(s.PathMap)
		if s.Credential != nil {
			value := *s.Credential
			s.Credential = &value
		}
	}
	return d
}

func public(d Document) Document {
	out := cloneDocument(d)
	for i := range out.MediaServers {
		if out.MediaServers[i].PathMap == nil {
			out.MediaServers[i].PathMap = map[string]string{}
		}
		out.MediaServers[i].HasCredential = out.MediaServers[i].Credential != nil && *out.MediaServers[i].Credential != ""
		out.MediaServers[i].Credential = nil
	}
	return out
}

// Get returns a private copy of the current document with credentials removed.
func (m *Manager) Get() Document {
	m.mu.Lock()
	defer m.mu.Unlock()
	return public(m.doc)
}

// Save validates and commits a document if its revision is still current.
func (m *Manager) Save(ctx context.Context, d Document) (Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.Revision != m.doc.Revision {
		return Document{}, ErrConflict
	}
	d = cloneDocument(d)
	bad := func(message string) (Document, error) { return Document{}, &ValidationError{message} }
	if d.Categories == nil || d.MediaServers == nil {
		return bad("categories and media_servers must be arrays")
	}
	old := map[string]Integration{}
	for _, s := range m.doc.MediaServers {
		old[s.ID] = s
	}
	seen := map[string]bool{}
	for i := range d.MediaServers {
		s := &d.MediaServers[i]
		if s.PathMap == nil {
			s.PathMap = map[string]string{}
		}
		if s.ID == "" || seen[s.ID] {
			return bad("media_servers: IDs must be nonempty and unique")
		}
		seen[s.ID] = true
		if previous, ok := old[s.ID]; ok && s.Credential == nil && previous.Type == s.Type {
			s.Credential = previous.Credential
		}
		if s.Credential == nil || *s.Credential == "" {
			return bad("media_servers: a credential is required for each connection")
		}
		for from, to := range s.PathMap {
			if !filepath.IsAbs(from) || !absoluteRemotePath(to) {
				return bad("media_servers.path_map: prefixes must be absolute paths")
			}
		}
	}
	for i := range d.Categories {
		cat := &d.Categories[i]
		if !filepath.IsAbs(cat.Path) {
			return bad("categories.path: enter an absolute server/container path")
		}
		cat.Path = filepath.Clean(cat.Path)
		if cat.Label == "" {
			cat.Label = cat.ID
		}
	}
	c := effective(m.cfg, d)
	if !m.cfg.YtdlpManaged() && d.Ytdlp.AutoUpdate != m.doc.Ytdlp.AutoUpdate {
		return bad("yt-dlp auto-update is only available for a managed binary")
	}
	if err := c.Validate(); err != nil {
		return bad(err.Error())
	}
	if err := c.ValidateDestinations(); err != nil {
		return bad(err.Error())
	}
	m.runtime.Gate.Lock()
	defer m.runtime.Gate.Unlock()
	for _, previous := range m.doc.Categories {
		next, exists := c.Category(previous.ID)
		if exists && next.Path == previous.Path && next.Layout == previous.Layout {
			continue
		}
		_, count, err := m.store.List(ctx, store.ListFilter{Category: previous.ID, Statuses: jobs.ActiveStatuses(), Limit: 1})
		if err != nil {
			return Document{}, err
		}
		if count > 0 {
			return Document{}, fmt.Errorf("%w: %s; finish or cancel its queued and running jobs first", ErrCategoryBusy, previous.ID)
		}
	}
	d.Revision++
	body, err := json.Marshal(d)
	if err != nil {
		return Document{}, err
	}
	if err := m.store.SaveSettings(ctx, m.doc.Revision, body); err != nil {
		if errors.Is(err, store.ErrStatusMismatch) {
			return Document{}, ErrConflict
		}
		return Document{}, err
	}
	m.runtime.Publish(c)
	// Retain our own copy; neither the request nor its mutable maps/slices
	// may alter the authoritative state after this method returns.
	m.doc = cloneDocument(d)
	if m.Changed != nil {
		m.Changed()
	}
	return public(d), nil
}
