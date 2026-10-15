package mediaserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// errNoSection means no Plex library section contains the folder.
var errNoSection = errors.New("no plex library section contains the folder")

// plex finds the library section that contains the folder and refreshes the
// folder inside it.
type plex struct {
	base  string
	token string
	paths pathMap
	hc    *http.Client
}

func (p *plex) name() string                 { return "plex" }
func (p *plex) mapPath(folder string) string { return p.paths.apply(folder) }

type sectionsResponse struct {
	MediaContainer struct {
		Directory []struct {
			Key      string `json:"key"`
			Location []struct {
				Path string `json:"path"`
			} `json:"Location"`
		} `json:"Directory"`
	} `json:"MediaContainer"`
}

func (p *plex) refresh(ctx context.Context, folder string) error {
	id, err := p.findSection(ctx, folder)
	if err != nil {
		return err
	}
	q := url.Values{"path": {folder}}
	u := p.base + "/library/sections/" + url.PathEscape(id) + "/refresh?" + q.Encode()
	resp, err := p.do(ctx, u)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("refresh section %s: unexpected status %s", id, resp.Status)
	}
	return nil
}

// findSection returns the key of the section with the longest location that
// contains folder (equal to it, or an ancestor at a segment boundary).
func (p *plex) findSection(ctx context.Context, folder string) (string, error) {
	resp, err := p.do(ctx, p.base+"/library/sections")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("list sections: unexpected status %s", resp.Status)
	}
	var sr sectionsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&sr); err != nil {
		return "", fmt.Errorf("list sections: %w", err)
	}
	best, bestLen := "", -1
	for _, d := range sr.MediaContainer.Directory {
		for _, l := range d.Location {
			loc := trimSlash(l.Path)
			if (folder == loc || strings.HasPrefix(folder, loc+"/")) && len(loc) > bestLen {
				best, bestLen = d.Key, len(loc)
			}
		}
	}
	if best == "" {
		return "", errNoSection
	}
	return best, nil
}

func (p *plex) do(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Plex-Token", p.token)
	req.Header.Set("Accept", "application/json")
	return p.hc.Do(req)
}
