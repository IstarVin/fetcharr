package naming

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func ip(i int) *int { return &i }

func TestSanitize(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "Dune Part Two", "Dune Part Two"},
		{"forbidden chars", `a<b>c:d"e/f\g|h?i*j`, "abcdefghij"},
		{"colon in title", "Star Wars: A New Hope", "Star Wars A New Hope"},
		{"collapse whitespace", "  a \t\n  b   c  ", "a b c"},
		{"spaces around removed char collapse", "a / b", "a b"},
		{"control chars", "a\x00b\x07c\x7fd", "abcd"},
		{"NEL is whitespace", "d\u0085e", "d e"},
		{"trailing dots", "Mr. Robot...", "Mr. Robot"},
		{"trailing dots and spaces", "name . . .  ", "name"},
		{"only dots", "...", ""},
		{"dot dot", "..", ""},
		{"traversal attempt", "../../etc/passwd", "....etcpasswd"},
		{"only forbidden", `<>:"/\|?*`, ""},
		{"empty", "", ""},
		{"whitespace only", " \t ", ""},
		{"unicode kept", "Amélie 千と千尋の神隠し", "Amélie 千と千尋の神隠し"},
		{"invalid utf8 dropped", "ab\xffcd", "abcd"},
		{"leading dot kept", ".hidden", ".hidden"},
		{"nbsp is whitespace", "a  b", "a b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sanitize(tc.in); got != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeTruncation(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantLen  int
		wantRune int // expected rune count, -1 = skip
	}{
		{"ascii exactly 180", strings.Repeat("a", 180), 180, 180},
		{"ascii 181", strings.Repeat("a", 181), 180, 180},
		{"ascii long", strings.Repeat("a", 1000), 180, 180},
		// 3-byte runes: 60 fit exactly
		{"cjk exact 180", strings.Repeat("千", 60), 180, 60},
		{"cjk 61 cuts at boundary", strings.Repeat("千", 61), 180, 60},
		// 2-byte runes with a 1-byte prefix: 1 + 2*89 = 179, next rune would be 181
		{"2-byte straddle", "a" + strings.Repeat("é", 100), 179, 90},
		// 4-byte runes (emoji): 45 fit exactly; boundary at 179 bytes of 3-byte..
		{"emoji", strings.Repeat("😀", 50), 180, 45},
		{"emoji straddle", "ab" + strings.Repeat("😀", 50), 178, 2 + 44},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Sanitize(tc.in)
			if len(got) != tc.wantLen {
				t.Errorf("len = %d, want %d", len(got), tc.wantLen)
			}
			if !utf8.ValidString(got) {
				t.Errorf("result is not valid UTF-8")
			}
			if tc.wantRune >= 0 && utf8.RuneCountInString(got) != tc.wantRune {
				t.Errorf("runes = %d, want %d", utf8.RuneCountInString(got), tc.wantRune)
			}
			if !strings.HasPrefix(tc.in, got) {
				t.Errorf("result is not a prefix of input")
			}
		})
	}
}

func TestSanitizeTruncationThenTrim(t *testing.T) {
	// The cut lands right after a space and a dot; they must be trimmed.
	in := strings.Repeat("a", 177) + " ..b"
	got := Sanitize(in)
	if got != strings.Repeat("a", 177) {
		t.Errorf("got %q (len %d)", got, len(got))
	}
}

func TestSanitizeNeverExceedsOrSplits(t *testing.T) {
	for n := 170; n < 200; n++ {
		for _, r := range []string{"a", "é", "千", "😀"} {
			got := Sanitize(strings.Repeat(r, n))
			if len(got) > MaxSegmentBytes || !utf8.ValidString(got) {
				t.Fatalf("%q x%d -> len %d valid %v", r, n, len(got), utf8.ValidString(got))
			}
		}
	}
}

func TestIsEmptyAfterSanitize(t *testing.T) {
	for in, want := range map[string]bool{"": true, "...": true, `<>|`: true, " ": true, "a": false, ".a": false} {
		if got := IsEmptyAfterSanitize(in); got != want {
			t.Errorf("IsEmptyAfterSanitize(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name      string
		layout    Layout
		m         Meta
		wantField string // "" = ok
		wantEmpty bool
	}{
		{"movie ok", LayoutMovie, Meta{Title: "Dune"}, "", false},
		{"movie missing title", LayoutMovie, Meta{}, "title", true},
		{"movie title only forbidden", LayoutMovie, Meta{Title: `?*:`}, "title", true},
		{"flat ok", LayoutFlat, Meta{Title: "Talk"}, "", false},
		{"flat dots", LayoutFlat, Meta{Title: "..."}, "title", true},
		{"tv ok no title", LayoutTV, Meta{ShowName: "Andor", Season: ip(1), Episode: ip(3)}, "", false},
		{"tv season 0 ok", LayoutTV, Meta{ShowName: "Andor", Season: ip(0), Episode: ip(0)}, "", false},
		{"tv missing show", LayoutTV, Meta{Season: ip(1), Episode: ip(1)}, "show_name", true},
		{"tv show sanitizes empty", LayoutTV, Meta{ShowName: "///", Season: ip(1), Episode: ip(1)}, "show_name", true},
		{"tv missing season", LayoutTV, Meta{ShowName: "A", Episode: ip(1)}, "season", false},
		{"tv missing episode", LayoutTV, Meta{ShowName: "A", Season: ip(1)}, "episode", false},
		{"tv negative season", LayoutTV, Meta{ShowName: "A", Season: ip(-1), Episode: ip(1)}, "season", false},
		{"tv negative episode", LayoutTV, Meta{ShowName: "A", Season: ip(1), Episode: ip(-1)}, "episode", false},
		{"tv bad episode title is not required", LayoutTV, Meta{ShowName: "A", Season: ip(1), Episode: ip(1), Title: "???"}, "", false},
		{"year 3 digits", LayoutMovie, Meta{Title: "A", Year: ip(999)}, "year", false},
		{"year 5 digits", LayoutMovie, Meta{Title: "A", Year: ip(10000)}, "year", false},
		{"year ok", LayoutMovie, Meta{Title: "A", Year: ip(2024)}, "", false},
		{"unknown layout", Layout("nope"), Meta{Title: "A"}, "category", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.layout, tc.m)
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want *ValidationError", err)
			}
			if ve.Field != tc.wantField {
				t.Errorf("field = %q, want %q", ve.Field, tc.wantField)
			}
			if errors.Is(err, ErrEmptyName) != tc.wantEmpty {
				t.Errorf("Is(ErrEmptyName) = %v, want %v", errors.Is(err, ErrEmptyName), tc.wantEmpty)
			}
		})
	}
}

func TestTargetPath(t *testing.T) {
	root := "/media/x"
	longShow := strings.Repeat("S", 300)
	longTitle := strings.Repeat("千", 100)
	tests := []struct {
		name   string
		layout Layout
		m      Meta
		ext    string
		want   string
	}{
		{"movie with year", LayoutMovie, Meta{Title: "Dune Part Two", Year: ip(2024)}, "mp4",
			"/media/x/Dune Part Two (2024)/Dune Part Two (2024).mp4"},
		{"movie no year", LayoutMovie, Meta{Title: "Dune"}, ".mkv",
			"/media/x/Dune/Dune.mkv"},
		{"movie sanitized", LayoutMovie, Meta{Title: "Mission: Impossible / 2.", Year: ip(1996)}, "mp4",
			"/media/x/Mission Impossible 2 (1996)/Mission Impossible 2 (1996).mp4"},
		{"flat", LayoutFlat, Meta{Title: "Conference Talk"}, "webm",
			"/media/x/Conference Talk.webm"},
		{"flat ignores year", LayoutFlat, Meta{Title: "Talk", Year: ip(2020)}, "webm",
			"/media/x/Talk.webm"},
		{"tv spec example", LayoutTV, Meta{ShowName: "Severance", Season: ip(2), Episode: ip(1), Title: "Hello, Ms. Cobel", Year: ip(2022)}, "mp4",
			"/media/x/Severance (2022)/Season 02/Severance - S02E01 - Hello, Ms. Cobel.mp4"},
		{"tv no title", LayoutTV, Meta{ShowName: "Andor", Season: ip(1), Episode: ip(3), Year: ip(2022)}, "mp4",
			"/media/x/Andor (2022)/Season 01/Andor - S01E03.mp4"},
		{"tv title only forbidden chars is omitted", LayoutTV, Meta{ShowName: "Andor", Season: ip(1), Episode: ip(3), Title: "???"}, "mp4",
			"/media/x/Andor/Season 01/Andor - S01E03.mp4"},
		{"tv no year", LayoutTV, Meta{ShowName: "Andor", Season: ip(1), Episode: ip(3), Title: "Rix"}, "mp4",
			"/media/x/Andor/Season 01/Andor - S01E03 - Rix.mp4"},
		{"tv season 0", LayoutTV, Meta{ShowName: "Doctor Who", Season: ip(0), Episode: ip(1), Title: "Special"}, "mp4",
			"/media/x/Doctor Who/Season 00/Doctor Who - S00E01 - Special.mp4"},
		{"tv episode 0", LayoutTV, Meta{ShowName: "A", Season: ip(1), Episode: ip(0)}, "mp4",
			"/media/x/A/Season 01/A - S01E00.mp4"},
		{"tv three digit episode", LayoutTV, Meta{ShowName: "One Piece", Season: ip(1), Episode: ip(100)}, "mkv",
			"/media/x/One Piece/Season 01/One Piece - S01E100.mkv"},
		{"tv three digit episode 999", LayoutTV, Meta{ShowName: "One Piece", Season: ip(1), Episode: ip(999)}, "mkv",
			"/media/x/One Piece/Season 01/One Piece - S01E999.mkv"},
		{"tv two digit stays two", LayoutTV, Meta{ShowName: "One Piece", Season: ip(1), Episode: ip(99)}, "mkv",
			"/media/x/One Piece/Season 01/One Piece - S01E99.mkv"},
		{"tv three digit season", LayoutTV, Meta{ShowName: "Show", Season: ip(100), Episode: ip(5)}, "mkv",
			"/media/x/Show/Season 100/Show - S100E05.mkv"},
		{"tv show with trailing dots", LayoutTV, Meta{ShowName: "Mr. Robot.", Season: ip(1), Episode: ip(1), Year: ip(2015)}, "mp4",
			"/media/x/Mr. Robot (2015)/Season 01/Mr. Robot - S01E01.mp4"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TargetPath(root, tc.layout, tc.m, tc.ext)
			if err != nil {
				t.Fatal(err)
			}
			if got != filepath.FromSlash(tc.want) {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}

	t.Run("long show and title stay within limits", func(t *testing.T) {
		tg, err := Resolve(root, LayoutTV, Meta{ShowName: longShow, Season: ip(1), Episode: ip(1), Title: longTitle})
		if err != nil {
			t.Fatal(err)
		}
		if len(tg.Base) > maxTVFileBase {
			t.Errorf("base is %d bytes", len(tg.Base))
		}
		if !utf8.ValidString(tg.Base) {
			t.Error("invalid utf8 in base")
		}
		if !strings.Contains(tg.Base, " - S01E01 - ") {
			t.Errorf("episode tag lost: %q", tg.Base)
		}
		for _, seg := range strings.Split(strings.TrimPrefix(tg.MediaPath("mp4"), root+"/"), "/") {
			if len(seg) > 255 {
				t.Errorf("segment %d bytes", len(seg))
			}
		}
	})

	t.Run("long movie title is cut at 180 bytes before the year", func(t *testing.T) {
		tg, err := Resolve(root, LayoutMovie, Meta{Title: strings.Repeat("é", 200), Year: ip(2000)})
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.Repeat("é", 90) + " (2000)"; tg.Base != want {
			t.Errorf("base = %q", tg.Base)
		}
	})

	t.Run("invalid meta", func(t *testing.T) {
		if _, err := TargetPath(root, LayoutMovie, Meta{Title: "..."}, "mp4"); !errors.Is(err, ErrEmptyName) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestResolveDirs(t *testing.T) {
	tg, err := Resolve("/r", LayoutTV, Meta{ShowName: "Severance", Season: ip(2), Episode: ip(1), Year: ip(2022)})
	if err != nil {
		t.Fatal(err)
	}
	if tg.Dir != filepath.FromSlash("/r/Severance (2022)/Season 02") || tg.RefreshDir != filepath.FromSlash("/r/Severance (2022)") || tg.Base != "Severance - S02E01" {
		t.Errorf("%+v", tg)
	}
	tg, _ = Resolve("/r", LayoutMovie, Meta{Title: "M", Year: ip(2000)})
	if tg.Dir != filepath.FromSlash("/r/M (2000)") || tg.RefreshDir != tg.Dir {
		t.Errorf("%+v", tg)
	}
	tg, _ = Resolve("/r", LayoutFlat, Meta{Title: "F"})
	if tg.Dir != "/r" || tg.RefreshDir != "/r" || tg.Base != "F" {
		t.Errorf("%+v", tg)
	}
}

func TestSubtitleNames(t *testing.T) {
	tests := []struct {
		name  string
		langs []string
		want  []string
	}{
		{"single", []string{"en"}, []string{"B.en.srt"}},
		{"two langs", []string{"en", "es"}, []string{"B.en.srt", "B.es.srt"}},
		{"dup lang", []string{"en", "en", "en"}, []string{"B.en.srt", "B.en.2.srt", "B.en.3.srt"}},
		{"dup interleaved", []string{"en", "es", "en"}, []string{"B.en.srt", "B.es.srt", "B.en.2.srt"}},
		{"missing lang is und", []string{"", "en"}, []string{"B.und.srt", "B.en.srt"}},
		{"two missing", []string{"", ""}, []string{"B.und.srt", "B.und.2.srt"}},
		{"case insensitive dup", []string{"en", "EN"}, []string{"B.en.srt", "B.EN.2.srt"}},
		{"hostile lang", []string{"../x"}, []string{"B.x.srt"}},
		{"lang only forbidden", []string{"///"}, []string{"B.und.srt"}},
		{"tag with dash kept", []string{"pt-BR"}, []string{"B.pt-BR.srt"}},
		{"long lang capped", []string{strings.Repeat("a", 100)}, []string{"B." + strings.Repeat("a", MaxLangBytes) + ".srt"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SubtitleNames("B", tc.langs)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWithin(t *testing.T) {
	tests := []struct {
		root, target string
		want         bool
	}{
		{"/m", "/m/a/b.mp4", true},
		{"/m/", "/m/a", true},
		{"/m", "/m", false},
		{"/m", "/m/../etc/x", false},
		{"/m", "/mx/a", false},
		{"/m", "/m/a/../../x", false},
		{"/m", "/m/a/../b", true},
		{"/m", "/other", false},
	}
	for _, tc := range tests {
		if got := Within(tc.root, tc.target); got != tc.want {
			t.Errorf("Within(%q,%q) = %v, want %v", tc.root, tc.target, got, tc.want)
		}
	}
}

func TestValidLayout(t *testing.T) {
	for l, want := range map[Layout]bool{LayoutMovie: true, LayoutTV: true, LayoutFlat: true, "": false, "Movie": false} {
		if ValidLayout(l) != want {
			t.Errorf("ValidLayout(%q)", l)
		}
	}
}

func TestNormalizeLangCapsLengthOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("é", 50) // 2 bytes per rune
	got := NormalizeLang(long)
	if len(got) > MaxLangBytes || !utf8.ValidString(got) || got == "" {
		t.Errorf("NormalizeLang = %q (%d bytes)", got, len(got))
	}
	// The longest sidecar name stays within NAME_MAX for the longest base.
	base := strings.Repeat("b", maxTVFileBase)
	name := SubtitleName(base, strings.Repeat("x", 500), 9999)
	if len(name) > 255 {
		t.Errorf("sidecar name is %d bytes", len(name))
	}
}
