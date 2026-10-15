// Package library holds the filesystem rules shared by the pre-check, the
// processing phase and the delete-file action: where a job's files live in
// its category (spec §5), the already_exists check, symlink-aware path
// containment, sidecar recognition and cleanup of files in the library.
package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/naming"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// videoExts are the extensions that count as "a video file" for the
// already_exists check (spec §4.1), lower case with the dot.
var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true,
	".wmv": true, ".flv": true, ".webm": true, ".ts": true, ".m2ts": true,
	".mts": true, ".mpg": true, ".mpeg": true, ".ogv": true, ".3gp": true,
	".divx": true, ".vob": true,
}

// IsVideoExt reports whether ext (with the dot, any case) is a video extension.
func IsVideoExt(ext string) bool { return videoExts[strings.ToLower(ext)] }

// ErrOutsideRoot is wrapped by the errors of the containment checks.
var ErrOutsideRoot = errors.New("path is outside the category root")

// MetaOf returns the naming metadata of a job.
func MetaOf(j *store.Job) naming.Meta {
	m := naming.Meta{Season: j.Season, Episode: j.Episode, Year: j.Year}
	if j.Title != nil {
		m.Title = *j.Title
	}
	if j.ShowName != nil {
		m.ShowName = *j.ShowName
	}
	return m
}

// TargetOf resolves the library location of job j in category cat.
func TargetOf(cat config.Category, j *store.Job) (naming.Target, error) {
	return naming.Resolve(cat.Path, cat.Layout, MetaOf(j))
}

// MediaFiles returns the video files in t.Dir whose name without extension is
// t.Base (any video extension), sorted by name. A missing folder is not an
// error.
func MediaFiles(t naming.Target) ([]string, error) {
	entries, err := os.ReadDir(t.Dir) // sorted by name
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if IsVideoExt(ext) && strings.TrimSuffix(e.Name(), ext) == t.Base {
			out = append(out, filepath.Join(t.Dir, e.Name()))
		}
	}
	return out, nil
}

// ExistingMedia returns the path of a video file in t.Dir whose name without
// extension is t.Base; "" if there is none (spec §4.1).
func ExistingMedia(t naming.Target) (string, error) {
	files, err := MediaFiles(t)
	if err != nil || len(files) == 0 {
		return "", err
	}
	return files[0], nil
}

// IsSidecar reports whether name is a subtitle sidecar of the media file
// whose name without extension is base: "<base>.<lang>.srt" or
// "<base>.<lang>.<n>.srt" (spec §5).
func IsSidecar(name, base string) bool {
	rest, ok := strings.CutPrefix(name, base+".")
	if !ok {
		return false
	}
	rest, ok = strings.CutSuffix(rest, ".srt")
	if !ok || rest == "" {
		return false
	}
	lang, n, hasN := strings.Cut(rest, ".")
	if lang == "" {
		return false
	}
	if !hasN {
		return true
	}
	if n == "" {
		return false
	}
	for _, r := range n {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Sidecars returns the subtitle sidecars of base in dir. A missing folder is
// not an error.
func Sidecars(dir, base string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && IsSidecar(e.Name(), base) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out, nil
}

// Resolve returns p with symlinks resolved, also when p does not exist (yet):
// the deepest existing ancestor is resolved with filepath.EvalSymlinks and the
// missing tail is appended unchanged. A dangling symlink counts as missing, so
// it is kept as a plain name (renaming over it replaces the link itself, it
// never writes through it).
func Resolve(p string) (string, error) {
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	var tail []string
	for cur := p; ; {
		r, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				r = filepath.Join(r, tail[i])
			}
			return r, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// ResolveDir verifies that dir is root or inside root, after cleaning and
// resolving symlinks in both (the deepest existing ancestor of each), and
// returns the resolved root and dir. A target that escapes through a symlinked
// folder fails with ErrOutsideRoot (spec §5, last rule).
func ResolveDir(root, dir string) (resolvedRoot, resolvedDir string, err error) {
	resolvedRoot, err = Resolve(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve category root: %w", err)
	}
	resolvedDir, err = Resolve(dir)
	if err != nil {
		return "", "", fmt.Errorf("resolve target folder: %w", err)
	}
	if resolvedDir != resolvedRoot && !naming.Within(resolvedRoot, resolvedDir) {
		return "", "", fmt.Errorf("%w: %s", ErrOutsideRoot, dir)
	}
	return resolvedRoot, resolvedDir, nil
}

// RemoveFiles deletes the media file at path and its subtitle sidecars, then
// removes the folders this leaves empty, up to but not including the category
// root (spec §6.3 DELETE ?delete_file=true). path must be inside root once
// symlinks are resolved, otherwise nothing is touched and ErrOutsideRoot is
// returned. Missing files are not an error. It returns the number of files
// removed.
func RemoveFiles(root, path string) (int, error) {
	dir, name := filepath.Split(filepath.Clean(path))
	if name == "" || name == "." || name == ".." {
		return 0, fmt.Errorf("%w: %s", ErrOutsideRoot, path)
	}
	// Only the folder is resolved: a media file that is itself a symlink is
	// removed as a link, never followed.
	resolvedRoot, resolvedDir, err := ResolveDir(root, dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	rm := func(p string) error {
		err := os.Remove(p)
		switch {
		case err == nil:
			removed++
		case errors.Is(err, os.ErrNotExist):
		default:
			return err
		}
		return nil
	}
	if err := rm(filepath.Join(resolvedDir, name)); err != nil {
		return removed, err
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	subs, err := Sidecars(resolvedDir, base)
	if err != nil {
		return removed, err
	}
	for _, s := range subs {
		if err := rm(s); err != nil {
			return removed, err
		}
	}
	// os.Remove only deletes empty folders: the first non-empty one (or any
	// error) ends the walk.
	for d := resolvedDir; d != resolvedRoot && naming.Within(resolvedRoot, d); d = filepath.Dir(d) {
		if os.Remove(d) != nil {
			break
		}
	}
	return removed, nil
}
