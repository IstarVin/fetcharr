package runner_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/resolve"
	"github.com/istarvin/fetcharr/server/internal/runner"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// hubHosts serves the saved 4KHDHub pages and the file behind the FSL
// mirror, so a resolver job runs without the network.
type hubHosts struct {
	t        *testing.T
	file     []byte
	fslFails atomic.Int32 // FSL requests left to fail
	fslHits  atomic.Int32
}

func (h *hubHosts) RoundTrip(req *http.Request) (*http.Response, error) {
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Request: req, Body: io.NopCloser(strings.NewReader(""))}
	page := func(name string) {
		b, err := os.ReadFile(filepath.Join("..", "resolve", "testdata", name))
		if err != nil {
			h.t.Error(err)
		}
		resp.Body = io.NopCloser(bytes.NewReader(b))
	}
	host := req.URL.Hostname()
	switch {
	case host == "greenmotors.club":
		page("redirect-greenmotors.html")
	case host == "hubcloud.ist":
		page("hubcloud-drive.html")
	case host == "gamerxyt.com":
		page("hubcloud-servers.html")
	case strings.HasSuffix(host, ".r2.cloudflarestorage.com"):
		h.fslHits.Add(1)
		if h.fslFails.Add(-1) >= 0 {
			resp.StatusCode = http.StatusServiceUnavailable
			break
		}
		resp.ContentLength = int64(len(h.file))
		resp.Body = io.NopCloser(bytes.NewReader(h.file))
	default: // 10Gbps hops and anything else: unreachable
		resp.StatusCode = http.StatusBadGateway
	}
	return resp, nil
}

func resolverRig(t *testing.T, hosts *hubHosts, retries int) (*rig, chan string) {
	files := make(chan string, 1)
	client := &http.Client{Transport: hosts}
	g := newRig(t, rigOpts{
		cfg: func(c *config.Config) { c.Downloads.AutoRetries = retries },
		mod: func(o *runner.Options) {
			o.HTTPClient = client
			o.Resolver = resolve.New(client)
			o.AutoRetryDelay = time.Millisecond
			o.Processor = funcProcessor(func(_ context.Context, _ *store.Job, file string) error {
				b, _ := os.ReadFile(file)
				if !bytes.Equal(b, hosts.file) {
					t.Errorf("downloaded content differs (%d bytes)", len(b))
				}
				files <- file
				return nil
			})
		},
	})
	return g, files
}

func TestResolverJobDownloadsFSLMirror(t *testing.T) {
	hosts := &hubHosts{t: t, file: append([]byte("\x1a\x45\xdf\xa3"), bytes.Repeat([]byte("x"), 50_000)...)}
	g, files := resolverRig(t, hosts, 0)
	j := g.create("https://greenmotors.club/?id=Q1dxTzFn")
	select {
	case f := <-files:
		if filepath.Base(f) != "media.mkv" {
			t.Errorf("file = %s", f)
		}
	case <-time.After(waitTimeout):
		t.Fatalf("not processed; status %s", g.get(j.ID).Status)
	}
}

func TestResolverJobRetriesAndResolvesAgain(t *testing.T) {
	hosts := &hubHosts{t: t, file: []byte("PK\x03\x04 archive")}
	hosts.fslFails.Store(1)
	g, files := resolverRig(t, hosts, 1)
	g.create("https://hubcloud.ist/drive/0lymmyaxdyc15gy")
	select {
	case f := <-files:
		if filepath.Base(f) != "media.zip" {
			t.Errorf("file = %s", f)
		}
	case <-time.After(waitTimeout):
		t.Fatal("not processed")
	}
	if hosts.fslHits.Load() != 2 {
		t.Errorf("FSL requests = %d, want 2", hosts.fslHits.Load())
	}
}

func TestResolverJobFailsWithoutLeakingURL(t *testing.T) {
	hosts := &hubHosts{t: t, file: []byte("x")}
	hosts.fslFails.Store(10)
	g, _ := resolverRig(t, hosts, 0)
	j := g.create("https://hubcloud.ist/drive/0lymmyaxdyc15gy")
	got := g.waitStatus(j.ID, jobs.StatusFailed)
	msg := deref(got.ErrorMessage)
	if deref(got.ErrorCode) != runner.CodeDownloadFailed || !strings.Contains(msg, "503") || strings.Contains(msg, "X-Amz") {
		t.Fatalf("error = %q %q", deref(got.ErrorCode), msg)
	}
}
