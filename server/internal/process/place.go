package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/istarvin/fetcharr/server/internal/library"
	"github.com/istarvin/fetcharr/server/internal/naming"
)

// place moves the subtitles and then the media file into the library (spec
// §4.3 step 3). The media file goes last: until then the work directory still
// holds it, so a failed placement can be retried, and the files placed so far
// are removed again. ctx only interrupts a cross-filesystem copy.
func (p *Processor) place(ctx context.Context, t naming.Target, mediaFile string, subs []subtitle, overwrite bool) (err error) {
	// MkdirAll follows symlinks. The folder was checked before (the deepest
	// existing ancestor is inside the root, the rest is new); check again
	// with the folder in place and work only with the resolved path.
	if err := os.MkdirAll(t.Dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", t.Dir, err)
	}
	_, dir, err := library.ResolveDir(t.Root, t.Dir)
	if err != nil {
		return err
	}

	langs := make([]string, len(subs))
	for i, s := range subs {
		langs[i] = s.lang
	}
	names := naming.SubtitleNames(t.Base, langs)
	media := filepath.Join(dir, t.Base+filepath.Ext(mediaFile))
	keep := map[string]bool{media: true}

	var placed []string
	defer func() {
		if err != nil {
			for _, f := range placed {
				_ = os.Remove(f)
			}
		}
	}()
	for i, s := range subs {
		dst := filepath.Join(dir, names[i])
		keep[dst] = true
		if err := p.move(ctx, s.path, dst); err != nil {
			return err
		}
		placed = append(placed, dst)
	}
	if err := p.move(ctx, mediaFile, media); err != nil {
		return err
	}

	if overwrite {
		p.removeStale(dir, t.Base, keep)
	}
	return nil
}

// removeStale deletes what an overwritten job left behind: video files with
// the same base name and another extension, and sidecars that the new job
// does not replace. Failures are logged only.
func (p *Processor) removeStale(dir, base string, keep map[string]bool) {
	media, err := library.MediaFiles(naming.Target{Dir: dir, Base: base})
	if err != nil {
		p.log.Warn("overwrite: list old media files", "err", err)
	}
	subs, err := library.Sidecars(dir, base)
	if err != nil {
		p.log.Warn("overwrite: list old subtitles", "err", err)
	}
	for _, f := range append(media, subs...) {
		if keep[f] {
			continue
		}
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			p.log.Warn("overwrite: delete old file", "err", err)
		}
	}
}

// move renames src to dst, or copies it through dst+".partial" when src and
// dst are on different filesystems, so the library never holds a half-written
// file. It replaces an existing dst.
func (p *Processor) move(ctx context.Context, src, dst string) error {
	err := p.rename(src, dst)
	if err != nil {
		if !errors.Is(err, syscall.EXDEV) {
			return err
		}
		if err := p.copyThenRename(ctx, src, dst); err != nil {
			return err
		}
	}
	// Media and sidecars get the same readable mode whatever created them.
	// Best effort: some network filesystems refuse chmod.
	if err := os.Chmod(dst, sidecarMode); err != nil {
		p.log.Debug("chmod placed file", "err", err)
	}
	return nil
}

func (p *Processor) copyThenRename(ctx context.Context, src, dst string) (err error) {
	partial := dst + ".partial"
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	// A stale or planted .partial (a symlink, say) is removed, never opened:
	// O_EXCL also refuses to follow a link created in between.
	if rerr := os.Remove(partial); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return rerr
	}
	out, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, sidecarMode)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = out.Close()
			_ = os.Remove(partial)
		}
	}()
	if _, err = io.Copy(out, &ctxReader{ctx: ctx, r: in}); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	return p.rename(partial, dst)
}

// ctxReader fails reads once ctx is done, which stops io.Copy.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(b []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, context.Cause(c.ctx)
	}
	return c.r.Read(b)
}
