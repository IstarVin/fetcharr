package mediaserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// jellyfin notifies Jellyfin through POST /Library/Media/Updated, which
// reports a changed path and makes the server rescan that folder.
type jellyfin struct {
	base  string
	key   string
	paths pathMap
	hc    *http.Client
}

func (j *jellyfin) name() string                 { return "jellyfin" }
func (j *jellyfin) mapPath(folder string) string { return j.paths.apply(folder) }

func (j *jellyfin) refresh(ctx context.Context, folder string) error {
	body, err := json.Marshal(map[string]any{
		"Updates": []map[string]string{{"Path": folder, "UpdateType": "Modified"}},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.base+"/Library/Media/Updated", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// The key goes in headers, never in the URL, so transport errors cannot
	// leak it into logs. Both forms are accepted by Jellyfin 10.x.
	req.Header.Set("X-Emby-Token", j.key)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=%q", j.key))
	resp, err := j.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return nil
}
