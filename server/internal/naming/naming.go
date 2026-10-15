// Package naming holds the pure functions that turn job metadata into
// Plex/Jellyfin-compatible library paths (spec §5). Nothing here touches the
// filesystem, so it is safe to call from request validation as well as from
// the processing pipeline.
package naming

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxSegmentBytes is the cap applied to every sanitized name segment.
const MaxSegmentBytes = 180

// maxTVFileBase bounds the base name (no extension) of a tv episode file. The
// show name and the episode title are each capped at MaxSegmentBytes, so
// their composition could exceed the 255 byte NAME_MAX of common filesystems;
// when it would, the episode title is shortened (never the S/E tag).
const maxTVFileBase = 225

// Layout is a category layout rule (spec §2.1).
type Layout string

// The three layouts of spec §2.1.
const (
	LayoutMovie Layout = "movie"
	LayoutTV    Layout = "tv"
	LayoutFlat  Layout = "flat"
)

// ValidLayout reports whether l is one of the three known layouts.
func ValidLayout(l Layout) bool {
	switch l {
	case LayoutMovie, LayoutTV, LayoutFlat:
		return true
	}
	return false
}

// ErrEmptyName is wrapped by validation errors for a required name that is
// empty (or empty after sanitizing). The API layer maps it to 400.
var ErrEmptyName = errors.New("name is empty after sanitizing")

// Meta is the naming metadata of one job. Nil pointers mean "not given".
type Meta struct {
	Title    string // movie / flat title, or tv episode title (optional for tv)
	ShowName string // tv only
	Season   *int   // tv only; 0 is valid (specials)
	Episode  *int   // tv only
	Year     *int   // optional; for tv it is the show's first-air year
}

// Sanitize makes s safe to use as one path segment (spec §5): removes
// < > : " / \ | ? * and control characters (and invalid UTF-8), collapses
// whitespace runs to one space, trims spaces and trailing dots, and caps the
// result at MaxSegmentBytes without splitting a UTF-8 character. The result
// may be empty.
func Sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r == utf8.RuneError && size <= 1 {
			continue // invalid byte: drop
		}
		switch {
		case strings.ContainsRune(`<>:"/\|?*`, r):
			continue
		case unicode.IsSpace(r):
			// Tabs/newlines are control characters but also whitespace;
			// treating them as whitespace gives the friendlier result.
			pendingSpace = true
			continue
		case unicode.IsControl(r):
			continue
		}
		if pendingSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pendingSpace = false
		b.WriteRune(r)
	}
	return trimEnd(truncateBytes(trimEnd(b.String()), MaxSegmentBytes))
}

// trimEnd removes trailing spaces and dots (leading spaces never occur after
// collapsing in Sanitize).
func trimEnd(s string) string { return strings.TrimRight(s, " .") }

// truncateBytes cuts s to at most limit bytes without splitting a rune.
func truncateBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// IsEmptyAfterSanitize reports whether s has no usable characters left once
// sanitized. The API layer rejects such a required name with 400.
func IsEmptyAfterSanitize(s string) bool { return Sanitize(s) == "" }

// ValidationError describes a Meta that cannot be named under a layout.
type ValidationError struct {
	Field string // JSON field name, e.g. "show_name"
	Msg   string
	err   error
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Msg }
func (e *ValidationError) Unwrap() error { return e.err }

func emptyErr(field string) error {
	return &ValidationError{Field: field, Msg: "is required and must not be empty after sanitizing", err: ErrEmptyName}
}

// Validate checks the layout's required fields (spec §2.1) and numeric ranges
// (spec §6.3): season >= 0, episode >= 0, year a 4-digit number. Required
// names must be non-empty after sanitizing (errors.Is(err, ErrEmptyName)).
// The returned error is a *ValidationError.
func Validate(layout Layout, m Meta) error {
	if !ValidLayout(layout) {
		return &ValidationError{Field: "category", Msg: fmt.Sprintf("unknown layout %q", layout)}
	}
	if m.Year != nil && (*m.Year < 1000 || *m.Year > 9999) {
		return &ValidationError{Field: "year", Msg: "must be a 4-digit number"}
	}
	switch layout {
	case LayoutMovie, LayoutFlat:
		if IsEmptyAfterSanitize(m.Title) {
			return emptyErr("title")
		}
	case LayoutTV:
		if IsEmptyAfterSanitize(m.ShowName) {
			return emptyErr("show_name")
		}
		if m.Season == nil {
			return &ValidationError{Field: "season", Msg: "is required"}
		}
		if *m.Season < 0 {
			return &ValidationError{Field: "season", Msg: "must be >= 0"}
		}
		if m.Episode == nil {
			return &ValidationError{Field: "episode", Msg: "is required"}
		}
		if *m.Episode < 0 {
			return &ValidationError{Field: "episode", Msg: "must be >= 0"}
		}
	}
	return nil
}

// Target is the resolved library location of a job, without file extension.
type Target struct {
	Root string // category root
	// Dir is the folder that holds the media file (and subtitles).
	Dir string
	// Base is the file name without extension; subtitles share it.
	Base string
	// RefreshDir is the folder to ask media servers to rescan (spec §4.5):
	// the show folder (tv), the movie folder (movie), or Root (flat).
	RefreshDir string
}

// MediaPath returns the media file path for container extension ext
// (with or without a leading dot).
func (t Target) MediaPath(ext string) string {
	return filepath.Join(t.Dir, t.Base+normExt(ext))
}

func normExt(ext string) string {
	ext = strings.TrimSpace(ext)
	if ext == "" || strings.HasPrefix(ext, ".") {
		return ext
	}
	return "." + ext
}

// Resolve computes the Target for a job (spec §5). It calls Validate first, so
// a non-nil error is a *ValidationError.
func Resolve(root string, layout Layout, m Meta) (Target, error) {
	if err := Validate(layout, m); err != nil {
		return Target{}, err
	}
	t := Target{Root: root}
	switch layout {
	case LayoutMovie:
		name := withYear(Sanitize(m.Title), m.Year)
		t.Dir = filepath.Join(root, name)
		t.Base = name
		t.RefreshDir = t.Dir
	case LayoutFlat:
		t.Dir = root
		t.Base = Sanitize(m.Title)
		t.RefreshDir = root
	case LayoutTV:
		show := Sanitize(m.ShowName)
		showDir := filepath.Join(root, withYear(show, m.Year))
		t.Dir = filepath.Join(showDir, "Season "+pad(*m.Season))
		t.RefreshDir = showDir
		t.Base = tvBase(show, *m.Season, *m.Episode, Sanitize(m.Title))
	}
	return t, nil
}

// TargetPath is shorthand for Resolve(...).MediaPath(ext).
func TargetPath(root string, layout Layout, m Meta, ext string) (string, error) {
	t, err := Resolve(root, layout, m)
	if err != nil {
		return "", err
	}
	return t.MediaPath(ext), nil
}

func withYear(name string, year *int) string {
	if year == nil || *year == 0 {
		return name
	}
	return name + " (" + strconv.Itoa(*year) + ")"
}

// pad zero-pads to two digits; numbers >= 100 naturally take three or more.
func pad(n int) string { return fmt.Sprintf("%02d", n) }

func tvBase(show string, season, episode int, title string) string {
	base := fmt.Sprintf("%s - S%sE%s", show, pad(season), pad(episode))
	if title == "" {
		return base
	}
	room := maxTVFileBase - len(base) - len(" - ")
	if room <= 0 {
		return base
	}
	title = trimEnd(truncateBytes(title, room))
	if title == "" {
		return base
	}
	return base + " - " + title
}

// SubtitleName returns the sidecar file name for the n-th (1-based) subtitle of
// a language: "<base>.<lang>.srt" for n == 1, "<base>.<lang>.<n>.srt"
// otherwise. lang is sanitized and dots are removed; an empty lang becomes
// "und".
func SubtitleName(base, lang string, n int) string {
	lang = NormalizeLang(lang)
	if n <= 1 {
		return base + "." + lang + ".srt"
	}
	return base + "." + lang + "." + strconv.Itoa(n) + ".srt"
}

// SubtitleNames returns one sidecar file name per language in input order,
// numbering repeated languages (case-insensitively) .2, .3, ...
func SubtitleNames(base string, langs []string) []string {
	seen := map[string]int{}
	out := make([]string, len(langs))
	for i, l := range langs {
		l = NormalizeLang(l)
		key := strings.ToLower(l)
		seen[key]++
		out[i] = SubtitleName(base, l, seen[key])
	}
	return out
}

// MaxLangBytes caps a normalized subtitle language tag. With the 225 byte
// maximum base name this keeps "<base>.<lang>.<n>.srt" within the 255 byte
// NAME_MAX for any realistic n.
const MaxLangBytes = 16

// NormalizeLang sanitizes a subtitle language tag for use in a file name and
// maps an empty one to "und" (spec §5, contract assumptions). The result is
// at most MaxLangBytes bytes long (cut on a UTF-8 boundary).
func NormalizeLang(lang string) string {
	lang = strings.ReplaceAll(Sanitize(lang), ".", "")
	lang = strings.ReplaceAll(lang, " ", "")
	lang = truncateBytes(lang, MaxLangBytes)
	if lang == "" {
		return "und"
	}
	return lang
}

// Within reports whether target, after lexical cleaning, is inside root (and
// not root itself). It does not resolve symlinks; the pipeline does that
// before placement (spec §5, last rule).
func Within(root, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
