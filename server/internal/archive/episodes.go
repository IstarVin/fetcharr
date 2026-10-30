package archive

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Episode is one extracted file and the episode number it is placed as.
type Episode struct {
	Path    string
	Episode int
}

// VideoFiles lists the video files under dir (recursively), without
// samples: names containing "sample", or files under a tenth of the median
// size when there are several.
func VideoFiles(dir string, isVideo func(ext string) bool) ([]string, error) {
	type file struct {
		path string
		size int64
	}
	var files []file
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() || !isVideo(filepath.Ext(p)) {
			return nil
		}
		if strings.Contains(strings.ToLower(filepath.Base(p)), "sample") {
			return nil
		}
		st, err := os.Stat(p)
		if err != nil {
			return err
		}
		files = append(files, file{p, st.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) > 2 {
		sizes := make([]int64, len(files))
		for i, f := range files {
			sizes[i] = f.size
		}
		slices.Sort(sizes)
		median := sizes[len(sizes)/2]
		files = slices.DeleteFunc(files, func(f file) bool { return f.size < median/10 })
	}
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out, nil
}

// Patterns for the episode number in a file name, most specific first.
var episodePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bS\d{1,3}[ ._-]?E(\d{1,4})`),
	regexp.MustCompile(`(?i)\b\d{1,2}x(\d{1,4})\b`),
	regexp.MustCompile(`(?i)\bEp?(?:isode)?[ ._-]?(\d{1,4})\b`),
	regexp.MustCompile(`\s-\s(\d{1,4})\b`),
}

// EpisodeNumber reads the episode number from a file name.
func EpisodeNumber(name string) (int, bool) {
	name = strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	for _, re := range episodePatterns {
		if m := re.FindStringSubmatch(name); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// ErrNoEpisodes is returned when an archive holds no video file.
var ErrNoEpisodes = errors.New("archive: no video files in the archive")

// MapEpisodes numbers the files: the episode read from each name plus
// offset. When a name carries no number or two files share one, every file
// is numbered by natural-sort position (1..N) plus offset instead.
func MapEpisodes(files []string, offset int) ([]Episode, error) {
	if len(files) == 0 {
		return nil, ErrNoEpisodes
	}
	out := make([]Episode, 0, len(files))
	seen := map[int]bool{}
	parsed := true
	for _, f := range files {
		n, ok := EpisodeNumber(f)
		if !ok || seen[n] {
			parsed = false
			break
		}
		seen[n] = true
		out = append(out, Episode{Path: f, Episode: n + offset})
	}
	if !parsed {
		sorted := slices.Clone(files)
		sort.Slice(sorted, func(i, j int) bool { return naturalLess(filepath.Base(sorted[i]), filepath.Base(sorted[j])) })
		out = out[:0]
		for i, f := range sorted {
			out = append(out, Episode{Path: f, Episode: i + 1 + offset})
		}
	}
	for _, e := range out {
		if e.Episode < 0 {
			return nil, fmt.Errorf("archive: episode offset %d gives a negative episode for %s", offset, filepath.Base(e.Path))
		}
	}
	slices.SortFunc(out, func(a, b Episode) int { return cmp.Compare(a.Episode, b.Episode) })
	return out, nil
}

var digitRun = regexp.MustCompile(`\d+|\D+`)

// naturalLess compares names with digit runs as numbers ("E2" < "E10").
func naturalLess(a, b string) bool {
	pa, pb := digitRun.FindAllString(strings.ToLower(a), -1), digitRun.FindAllString(strings.ToLower(b), -1)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] == pb[i] {
			continue
		}
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		if ea == nil && eb == nil && na != nb {
			return na < nb
		}
		return pa[i] < pb[i]
	}
	return len(pa) < len(pb)
}
