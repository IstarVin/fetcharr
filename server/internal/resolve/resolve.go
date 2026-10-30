// Package resolve turns 4KHDHub file links into direct file URLs just before
// a download starts. A job keeps the link it was created with (the
// greenmotors redirector, a HubDrive or a HubCloud page); the signed file URLs
// behind it expire after a few hours, so every attempt resolves again.
//
// The chain is: redirector page (payload decoded offline) -> HubDrive file
// page (one button to HubCloud) -> HubCloud drive page -> server list ->
// FSL (presigned R2, resumable), 10Gbps (Google file behind redirects, no
// Range) or Pixeldrain. The hosts rotate their TLDs, so only the subdomain is
// matched.
package resolve

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// maxPage caps the bytes read from one interstitial page.
const maxPage = 4 << 20

// maxHops bounds the redirector -> HubDrive -> HubCloud chain.
const maxHops = 5

// userAgent is sent on every request; the hosts answer plain clients too,
// but a browser UA keeps the ad interstitials out of bot heuristics.
const userAgent = "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36"

// hostPrefixes are the subdomains of the links this package accepts.
var hostPrefixes = []string{"greenmotors.", "hubcloud.", "hubdrive."}

// Hosts lists the accepted host prefixes without the dot (for /api/info).
func Hosts() []string {
	out := make([]string, len(hostPrefixes))
	for i, p := range hostPrefixes {
		out[i] = strings.TrimSuffix(p, ".")
	}
	return out
}

// directMirror returns the source for a URL that is already a mirror file: a
// presigned R2 object or a Pixeldrain API file. A client may resolve a link
// itself (a phone's network is not blocked the way a server's can be) and send
// that URL. It is not given to yt-dlp, which rejects archives and
// extension-less objects (see package direct).
func directMirror(raw string) (Source, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return Source{}, false
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case strings.HasSuffix(host, ".r2.cloudflarestorage.com") && u.Query().Has("X-Amz-Signature"):
		return Source{Server: "FSL", URL: raw, Resumable: true}, true
	case host == "pixeldrain.com" && strings.HasPrefix(u.Path, "/api/file/"):
		return Source{Server: "Pixeldrain", URL: raw, Resumable: true}, true
	}
	return Source{}, false
}

// Matches reports whether raw is a link this package resolves.
func Matches(raw string) bool {
	if _, ok := directMirror(raw); ok {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, p := range hostPrefixes {
		if strings.HasPrefix(host, p) {
			return true
		}
	}
	return false
}

// Error is a resolution failure. Its message never contains a URL. It is
// retryable: the hosts are flaky and a later attempt often succeeds.
type Error struct{ Message string }

func (e *Error) Error() string { return "resolve: " + e.Message }

func fail(format string, args ...any) error { return &Error{Message: fmt.Sprintf(format, args...)} }

// Source is one downloadable mirror of the file.
type Source struct {
	Server string // "FSL", "10Gbps", "Pixeldrain"
	URL    string // signed; never log it
	// Resumable is false for mirrors known to ignore Range.
	Resumable bool
}

// Resolver resolves links. The zero value is not usable; call New.
type Resolver struct {
	client *http.Client
}

// New returns a Resolver using client, or a default client with a 30 s
// timeout when client is nil.
func New(client *http.Client) *Resolver {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Resolver{client: client}
}

// Resolve returns the mirrors of the file behind raw, best first. It fails
// when no mirror could be resolved.
func (r *Resolver) Resolve(ctx context.Context, raw string) ([]Source, error) {
	if s, ok := directMirror(raw); ok {
		return []Source{s}, nil
	}
	current := raw
	for range maxHops {
		u, err := url.Parse(current)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fail("invalid link")
		}
		host := strings.ToLower(u.Hostname())
		switch {
		case strings.HasPrefix(host, "hubcloud."):
			return r.hubcloud(ctx, current)
		case strings.HasPrefix(host, "hubdrive."):
			page, err := r.get(ctx, current, "")
			if err != nil {
				return nil, err
			}
			next := HubDriveTarget(page)
			if next == "" {
				return nil, fail("HubDrive page has no HubCloud link")
			}
			current = next
		default:
			page, err := r.get(ctx, current, "")
			if err != nil {
				return nil, err
			}
			next, ok := DecodeRedirect(page)
			if !ok {
				return nil, fail("unrecognized link page")
			}
			current = next
		}
	}
	return nil, fail("too many hops")
}

func (r *Resolver) hubcloud(ctx context.Context, drive string) ([]Source, error) {
	page, err := r.get(ctx, drive, "")
	if err != nil {
		return nil, err
	}
	next := HubCloudNext(page)
	if next == "" {
		return nil, fail("HubCloud page has no download button")
	}
	list, err := r.get(ctx, next, drive)
	if err != nil {
		return nil, err
	}
	var out []Source
	for _, m := range Mirrors(list) {
		switch {
		case strings.HasPrefix(strings.ToUpper(m.Server), "FSL"):
			// HubCloud's FSL is a presigned r2.cloudflarestorage.com URL;
			// public *.r2.dev mirrors never connect.
			if u, err := url.Parse(m.URL); err == nil && !strings.HasSuffix(u.Hostname(), ".r2.dev") {
				out = append(out, Source{Server: "FSL", URL: m.URL, Resumable: true})
			}
		case strings.Contains(strings.ToLower(m.Server), "10gbps"):
			if file, err := r.redirectedFile(ctx, m.URL); err == nil {
				out = append(out, Source{Server: "10Gbps", URL: file})
			}
		case strings.Contains(strings.ToLower(m.Server), "pixel"):
			if file, err := r.pixeldrain(ctx, m.URL); err == nil {
				out = append(out, Source{Server: "Pixeldrain", URL: file, Resumable: true})
			}
		}
	}
	// FSL first (resumable), then Pixeldrain, then 10Gbps (restarts on
	// every retry).
	order := map[string]int{"FSL": 0, "Pixeldrain": 1, "10Gbps": 2}
	slices.SortStableFunc(out, func(a, b Source) int { return order[a.Server] - order[b.Server] })
	if len(out) == 0 {
		return nil, fail("HubCloud offered no usable server")
	}
	return out, nil
}

// redirectedFile follows the 10Gbps hops (two 302s) to the dl.php?link=
// ad wrapper and returns the file it wraps.
func (r *Resolver) redirectedFile(ctx context.Context, start string) (string, error) {
	noFollow := *r.client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	current := start
	for range 6 {
		if file, ok := dlLink(current); ok {
			return file, nil
		}
		req, err := r.request(ctx, current, "")
		if err != nil {
			return "", err
		}
		resp, err := noFollow.Do(req)
		if err != nil {
			return "", fail("10Gbps server unreachable")
		}
		_ = resp.Body.Close()
		loc := resp.Header.Get("Location")
		if loc == "" {
			return "", fail("10Gbps server did not redirect (HTTP %d)", resp.StatusCode)
		}
		base, _ := url.Parse(current)
		next, err := base.Parse(loc)
		if err != nil {
			return "", fail("10Gbps server sent an invalid redirect")
		}
		current = next.String()
	}
	return "", fail("10Gbps server redirected too often")
}

func dlLink(s string) (string, bool) {
	_, file, ok := strings.Cut(s, "dl.php?link=")
	if !ok || file == "" {
		return "", false
	}
	if !strings.Contains(file, "://") {
		if unescaped, err := url.QueryUnescape(file); err == nil {
			file = unescaped
		}
	}
	return file, strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://")
}

var pixeldrainID = regexp.MustCompile(`/(?:u|api/file)/([A-Za-z0-9]+)`)

// pixeldrain checks that the file may be downloaded (taken-down files answer
// 451) and returns its API download URL.
func (r *Resolver) pixeldrain(ctx context.Context, link string) (string, error) {
	m := pixeldrainID.FindStringSubmatch(link)
	if m == nil {
		return "", fail("unknown Pixeldrain link")
	}
	body, err := r.get(ctx, "https://pixeldrain.com/api/file/"+m[1]+"/info", "")
	if err != nil {
		return "", err
	}
	var info struct {
		CanDownload bool `json:"can_download"`
	}
	if json.Unmarshal([]byte(body), &info) != nil || !info.CanDownload {
		return "", fail("Pixeldrain file unavailable")
	}
	return "https://pixeldrain.com/api/file/" + m[1], nil
}

func (r *Resolver) request(ctx context.Context, raw, referer string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, fail("invalid link")
	}
	req.Header.Set("User-Agent", userAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	return req, nil
}

// get fetches a page. Errors name the host only.
func (r *Resolver) get(ctx context.Context, raw, referer string) (string, error) {
	req, err := r.request(ctx, raw, referer)
	if err != nil {
		return "", err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", context.Cause(ctx)
		}
		return "", fail("%s unreachable", req.URL.Hostname())
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fail("%s answered HTTP %d", req.URL.Hostname(), resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxPage))
	if err != nil {
		if ctx.Err() != nil {
			return "", context.Cause(ctx)
		}
		return "", fail("%s: reading the page failed", req.URL.Hostname())
	}
	return string(b), nil
}

var (
	redirectPayload = regexp.MustCompile(`s\('o','([A-Za-z0-9+/=]+)'`)
	anchor          = regexp.MustCompile(`(?is)<a\s([^>]*)>(.*?)</a>`)
	hrefAttr        = regexp.MustCompile(`(?is)\bhref\s*=\s*["']([^"']+)["']`)
	idDownload      = regexp.MustCompile(`(?is)\bid\s*=\s*["']download["']`)
	jsURL           = regexp.MustCompile(`var url = '([^']+)'`)
	tag             = regexp.MustCompile(`<[^>]*>`)
	spaces          = regexp.MustCompile(`\s+`)
)

// DecodeRedirect returns the target of a redirector page: the s('o', …)
// payload is base64, base64, ROT13 and base64 again, giving
// {"o":"<base64 url>"}.
func DecodeRedirect(page string) (string, bool) {
	m := redirectPayload.FindStringSubmatch(page)
	if m == nil {
		return "", false
	}
	step, ok := b64(m[1])
	if !ok {
		return "", false
	}
	if step, ok = b64(step); !ok {
		return "", false
	}
	if step, ok = b64(rot13(step)); !ok {
		return "", false
	}
	var payload struct {
		O string `json:"o"`
	}
	if json.Unmarshal([]byte(step), &payload) != nil || payload.O == "" {
		return "", false
	}
	target, ok := b64(payload.O)
	if !ok || !strings.HasPrefix(target, "http") {
		return "", false
	}
	return target, true
}

func b64(s string) (string, bool) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), true
		}
	}
	return "", false
}

func rot13(s string) string {
	return strings.Map(func(c rune) rune {
		switch {
		case c >= 'a' && c <= 'z':
			return 'a' + (c-'a'+13)%26
		case c >= 'A' && c <= 'Z':
			return 'A' + (c-'A'+13)%26
		}
		return c
	}, s)
}

type link struct{ attrs, href, text string }

func links(page string) []link {
	var out []link
	for _, m := range anchor.FindAllStringSubmatch(page, -1) {
		h := hrefAttr.FindStringSubmatch(m[1])
		if h == nil {
			continue
		}
		text := html.UnescapeString(tag.ReplaceAllString(m[2], " "))
		text = strings.TrimSpace(spaces.ReplaceAllString(strings.ReplaceAll(text, "⚡", ""), " "))
		out = append(out, link{attrs: m[1], href: html.UnescapeString(h[1]), text: text})
	}
	return out
}

func isHubCloud(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && strings.HasPrefix(strings.ToLower(u.Hostname()), "hubcloud.")
}

// HubDriveTarget returns the HubCloud drive linked from a HubDrive file page.
func HubDriveTarget(page string) string {
	for _, l := range links(page) {
		if isHubCloud(l.href) {
			return l.href
		}
	}
	return ""
}

// HubCloudNext returns the server-list URL of a HubCloud drive or video
// page: the #download button, else `var url = '…'`, else a hubcloud.php link.
func HubCloudNext(page string) string {
	all := links(page)
	for _, l := range all {
		if idDownload.MatchString(l.attrs) && strings.HasPrefix(l.href, "http") {
			return l.href
		}
	}
	if m := jsURL.FindStringSubmatch(page); m != nil && strings.HasPrefix(m[1], "http") {
		return m[1]
	}
	for _, l := range all {
		if strings.Contains(l.href, "hubcloud.php") && strings.HasPrefix(l.href, "http") {
			return l.href
		}
	}
	return ""
}

// Mirror is one "Download [<server>]" button of the server list.
type Mirror struct{ Server, URL string }

// Mirrors returns the server buttons of a HubCloud server list page.
func Mirrors(page string) []Mirror {
	var out []Mirror
	for _, l := range links(page) {
		if !strings.HasPrefix(l.text, "Download [") || !strings.HasPrefix(l.href, "http") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(l.text, "Download ["), "]")
		out = append(out, Mirror{Server: strings.TrimSpace(name), URL: l.href})
	}
	return out
}

// IsRetryable reports whether err is a resolution failure worth retrying.
func IsRetryable(err error) bool {
	var e *Error
	return errors.As(err, &e)
}
