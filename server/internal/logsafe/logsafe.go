// Package logsafe holds the helpers that keep URLs and transport errors safe
// to log (spec §9: URLs are logged without their query string; tokens, API
// keys and header values are never logged).
package logsafe

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Patterns for the parts of an http(s) URL embedded in text that must not be
// logged: userinfo, query string and fragment.
var (
	userinfo = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/?#"'<>@]+@`)
	query    = regexp.MustCompile(`(?i)(https?://[^\s?#"'<>]+)\?[^\s"'<>]*`)
	fragment = regexp.MustCompile(`(?i)(https?://[^\s#"'<>]+)#[^\s"'<>]*`)
)

// URL returns raw without userinfo, query string and fragment. A value that is
// not an absolute URL yields "<invalid url>" rather than being echoed.
func URL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<invalid url>"
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
	return u.String()
}

// Raw is for a value that was meant to be a URL but may be malformed (a
// config value in a validation error): everything from the first '?' or '#' on
// and any userinfo is dropped, whatever the scheme.
func Raw(s string) string {
	s, _, _ = strings.Cut(s, "?")
	s, _, _ = strings.Cut(s, "#")
	return userinfo.ReplaceAllString(s, "$1")
}

// Text removes the userinfo, query string and fragment of every http(s) URL
// embedded in s.
func Text(s string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	s = userinfo.ReplaceAllString(s, "$1")
	s = query.ReplaceAllString(s, "$1")
	return fragment.ReplaceAllString(s, "$1")
}

type safeError struct {
	msg string
	err error
}

func (e *safeError) Error() string { return e.msg }
func (e *safeError) Unwrap() error { return e.err }

// Err returns an error whose message is err's without the request URL that
// *url.Error embeds (net/http errors include the full URL, query string
// included) and without the query string of any other URL in the text.
// The surrounding wrapping text is kept. Err(nil) is nil.
func Err(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) {
		msg = strings.ReplaceAll(msg, ue.Error(), ue.Err.Error())
	}
	return &safeError{msg: Text(msg), err: err}
}
