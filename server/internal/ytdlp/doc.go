// Package ytdlp builds yt-dlp command lines (spec §4.2), parses its progress
// output, captures and redacts its error messages, and runs it as a
// process-group-managed subprocess (spec §4.4).
//
// The package knows nothing about jobs, stores or the dispatcher: callers pass
// plain values in and get plain values (or typed errors) back.
package ytdlp
