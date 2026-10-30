// Package archive extracts the season packs of archive jobs (zip, tar and
// 7z) and maps the extracted episode files to episode numbers.
//
// zip and tar use the standard library; 7z runs the 7-Zip binary (7zz or
// 7z), so it is offered only when one is on PATH. Every entry is confined to
// the destination folder and links are skipped.
package archive

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Format is an archive format.
type Format string

// The supported formats.
const (
	Zip      Format = "zip"
	Tar      Format = "tar"
	SevenZip Format = "7z"
)

// ErrUnsupported is returned for a file that is not a supported archive.
var ErrUnsupported = errors.New("archive: not a zip, tar or 7z archive")

// Detect reads the magic bytes of the file at p.
func Detect(p string) (Format, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch {
	case len(head) >= 4 && (string(head[:4]) == "PK\x03\x04" || string(head[:4]) == "PK\x05\x06"):
		return Zip, nil
	case len(head) >= 6 && string(head[:6]) == "7z\xbc\xaf\x27\x1c":
		return SevenZip, nil
	case len(head) >= 262 && string(head[257:262]) == "ustar":
		return Tar, nil
	}
	return "", ErrUnsupported
}

// SevenZipBinary returns the 7-Zip executable on PATH, or "".
func SevenZipBinary() string {
	for _, name := range []string{"7zz", "7z"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// Formats lists the formats this server can extract.
func Formats() []string {
	out := []string{string(Zip), string(Tar)}
	if SevenZipBinary() != "" {
		out = append(out, string(SevenZip))
	}
	return out
}

// Progress receives the bytes extracted so far and the expected total
// (total may be an estimate; 0 when unknown).
type Progress func(done, total int64)

// Extract unpacks the archive src into the folder dst (created if needed).
func Extract(ctx context.Context, src, dst string, onProgress Progress) error {
	format, err := Detect(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	dst, err = filepath.Abs(dst)
	if err != nil {
		return err
	}
	if onProgress == nil {
		onProgress = func(int64, int64) {}
	}
	switch format {
	case Zip:
		return extractZip(ctx, src, dst, onProgress)
	case Tar:
		return extractTar(ctx, src, dst, onProgress)
	default:
		return extract7z(ctx, src, dst, onProgress)
	}
}

// UncompressedSize is the space the extracted files need: exact for zip,
// the archive size otherwise (episode packs are stored, not compressed).
func UncompressedSize(src string) (int64, error) {
	format, err := Detect(src)
	if err != nil {
		return 0, err
	}
	if format == Zip {
		r, err := zip.OpenReader(src)
		if err != nil {
			return 0, fmt.Errorf("archive: open zip: %w", err)
		}
		defer func() { _ = r.Close() }()
		var n int64
		for _, f := range r.File {
			n += int64(f.UncompressedSize64)
		}
		return n, nil
	}
	st, err := os.Stat(src)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// within returns the absolute path of entry name under dst, or an error
// when it would escape dst (zip-slip).
func within(dst, name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("archive: absolute entry path %q", name)
	}
	p := filepath.Join(dst, filepath.FromSlash(name))
	rel, err := filepath.Rel(dst, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive: entry %q escapes the folder", name)
	}
	return p, nil
}

// counter reports copied bytes at most every 250 ms.
type counter struct {
	ctx    context.Context
	done   int64
	total  int64
	last   time.Time
	report Progress
}

func (c *counter) add(n int) error {
	c.done += int64(n)
	if now := time.Now(); now.Sub(c.last) >= 250*time.Millisecond {
		c.last = now
		c.report(c.done, c.total)
	}
	if c.ctx.Err() != nil {
		return context.Cause(c.ctx)
	}
	return nil
}

func (c *counter) copyFile(p string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				_ = f.Close()
				return err
			}
			if err := c.add(n); err != nil {
				_ = f.Close()
				return err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			_ = f.Close()
			return fmt.Errorf("archive: read entry: %w", rerr)
		}
	}
	return f.Close()
}

func extractZip(ctx context.Context, src, dst string, report Progress) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("archive: open zip: %w", err)
	}
	defer func() { _ = r.Close() }()
	c := &counter{ctx: ctx, report: report}
	for _, f := range r.File {
		c.total += int64(f.UncompressedSize64)
	}
	for _, f := range r.File {
		p, err := within(dst, f.Name)
		if err != nil {
			return err
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(p, 0o750); err != nil {
				return err
			}
			continue
		case !mode.IsRegular():
			continue // links and devices are never extracted
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("archive: open entry: %w", err)
		}
		err = c.copyFile(p, rc)
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	report(c.done, c.total)
	return nil
}

func extractTar(ctx context.Context, src, dst string, report Progress) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	c := &counter{ctx: ctx, report: report, total: st.Size()}
	tr := tar.NewReader(bufio.NewReaderSize(f, 1<<20))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("archive: read tar: %w", err)
		}
		p, err := within(dst, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := c.copyFile(p, tr); err != nil {
				return err
			}
		}
	}
	report(c.done, c.total)
	return nil
}

var sevenZipPercent = regexp.MustCompile(`(\d{1,3})%`)

func extract7z(ctx context.Context, src, dst string, report Progress) error {
	bin := SevenZipBinary()
	if bin == "" {
		return errors.New("archive: 7z archives need 7-Zip (7zz or 7z) on the server")
	}
	var total int64
	if st, err := os.Stat(src); err == nil {
		total = st.Size()
	}
	// 7-Zip refuses entries that escape -o; links it may create are removed
	// below. -bsp1 prints progress, -bse1 sends errors to the same pipe.
	cmd := exec.CommandContext(ctx, bin, "x", "-y", "-bso0", "-bse1", "-bsp1", "-o"+dst, "--", src) //nolint:gosec // fixed binary and arguments
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("archive: start 7-Zip: %w", err)
	}
	var lastLine string
	sc := bufio.NewScanner(out)
	sc.Split(scanCRLF)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := sevenZipPercent.FindStringSubmatch(line); m != nil && total > 0 {
			pct, _ := strconv.Atoi(m[1])
			report(total*int64(pct)/100, total)
		} else if line != "" {
			lastLine = line
		}
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if err != nil {
		return fmt.Errorf("archive: 7-Zip failed: %s", lastLine)
	}
	report(total, total)
	return removeLinks(dst)
}

// scanCRLF splits on \n and \r: 7-Zip redraws its progress line with \r
// and backspaces.
func scanCRLF(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' || b == '\r' || b == '\b' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// removeLinks deletes symlinks an external extractor may have created.
func removeLinks(dst string) error {
	return filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return os.Remove(p)
		}
		return nil
	})
}
