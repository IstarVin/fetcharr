package runner

import (
	"errors"
	"testing"

	"github.com/istarvin/fetcharr/server/internal/ytdlp"
)

func TestRetryableYtdlpErrors(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"[generic] Unsupported URL: https://example.com/x", false},
		{"Video unavailable", false},
		{"[youtube] abc: Video unavailable. This video is private", false},
		{"This video is private", false},
		{"[youtube] abc: This video has been removed by the uploader", false},
		{"The uploader has not made this video available in your country", false},
		{"HTTP Error 404: Not Found", false},
		{"unable to download video data: HTTP Error 403: Forbidden", true},
		{"HTTP Error 410: Gone", false},
		{"HTTP Error 503: Service Unavailable", true},
		{"HTTP Error 429: Too Many Requests", true},
		{"Unable to download webpage: <urlopen error timed out>", true},
		{"exited with code 1", true},
		{"", true},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			if got := retryable(&ytdlp.Error{Message: tt.msg, ExitCode: 1}); got != tt.want {
				t.Errorf("retryable(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
	if retryable(errors.New("something else")) {
		t.Error("unknown non-yt-dlp error must not be retried")
	}
}
