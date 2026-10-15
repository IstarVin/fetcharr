package logsafe

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestURL(t *testing.T) {
	cases := map[string]string{
		"https://u:p@h.example/a/b?token=S#frag": "https://h.example/a/b",
		"http://h.example:8080/x?":               "http://h.example:8080/x",
		"not a url":                              "<invalid url>",
		"":                                       "<invalid url>",
		"/relative?token=S":                      "<invalid url>",
	}
	for in, want := range cases {
		if got := URL(in); got != want {
			t.Errorf("URL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRaw(t *testing.T) {
	got := Raw("ftp://u:p@h.example/x?api_key=S#f")
	if got != "ftp://h.example/x" {
		t.Errorf("Raw = %q", got)
	}
}

func TestText(t *testing.T) {
	in := `GET "https://u:p@h.example/a?token=S&x=1#f" and http://h2.example/b#frag, plain`
	got := Text(in)
	for _, bad := range []string{"token", "S&", "u:p", "#f", "frag"} {
		if strings.Contains(got, bad) {
			t.Errorf("Text left %q in %q", bad, got)
		}
	}
	if !strings.Contains(got, "https://h.example/a") || !strings.Contains(got, "http://h2.example/b") {
		t.Errorf("Text dropped the URL itself: %q", got)
	}
	if Text("no urls here") != "no urls here" {
		t.Error("Text changed plain text")
	}
}

func TestErr(t *testing.T) {
	if Err(nil) != nil {
		t.Fatal("Err(nil) != nil")
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:1/a?token=SECRET", nil)
	resp, cerr := http.DefaultClient.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if cerr == nil || !strings.Contains(cerr.Error(), "SECRET") {
		t.Fatalf("precondition: %v", cerr)
	}
	wrapped := fmt.Errorf("refresh: %w", cerr)
	got := Err(wrapped)
	if strings.Contains(got.Error(), "SECRET") || strings.Contains(got.Error(), "127.0.0.1:1/a") {
		t.Errorf("Err leaked the URL: %v", got)
	}
	if !strings.HasPrefix(got.Error(), "refresh: ") {
		t.Errorf("Err lost the wrapping text: %v", got)
	}
	if !errors.Is(got, wrapped) {
		t.Error("Err broke the error chain")
	}
	plain := Err(errors.New("see https://h.example/x?k=SECRET"))
	if strings.Contains(plain.Error(), "SECRET") {
		t.Errorf("Err leaked a query: %v", plain)
	}
}
