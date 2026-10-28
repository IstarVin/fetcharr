package notify

import (
	"fmt"
	"strings"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// errorTexts match the clients' short failure reasons.
var errorTexts = map[string]string{
	"low_disk_space":    "Not enough disk space on server",
	"already_exists":    "File already exists in library",
	"download_failed":   "Download failed",
	"processing_failed": "Couldn't save file",
}

// jobTitle is "Show — S02E03", "Title (Year)" or "Title", like the clients.
func jobTitle(j *store.Job) string {
	if j.ShowName != nil && j.Season != nil && j.Episode != nil {
		return fmt.Sprintf("%s — S%02dE%02d", *j.ShowName, *j.Season, *j.Episode)
	}
	title := ""
	if j.Title != nil {
		title = *j.Title
	}
	if title == "" && j.ShowName != nil {
		title = *j.ShowName
	}
	if title == "" {
		title = "Download"
	}
	if j.Year != nil {
		return fmt.Sprintf("%s (%d)", title, *j.Year)
	}
	return title
}

func jobMessage(j *store.Job) Message {
	if j.Status == jobs.StatusCompleted {
		return Message{Event: EventCompleted, Title: "Downloaded", Body: jobTitle(j), Job: j}
	}
	reason := "Download failed"
	if j.ErrorCode != nil {
		if text, ok := errorTexts[*j.ErrorCode]; ok {
			reason = text
		}
	}
	return Message{Event: EventFailed, Title: "Failed", Body: jobTitle(j) + " — " + reason, Job: j}
}

// batchMessage summarizes a finished batch: "Severance S02 — 8 downloaded, 1 failed".
func batchMessage(j *store.Job, completed, failed int) Message {
	name := "Season"
	if j.ShowName != nil {
		name = *j.ShowName
	}
	if j.Season != nil {
		name += fmt.Sprintf(" S%02d", *j.Season)
	}
	var parts []string
	if completed > 0 {
		parts = append(parts, fmt.Sprintf("%d downloaded", completed))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	return Message{
		Event: EventBatchFinished, Title: "Season finished", Body: name + " — " + strings.Join(parts, ", "),
		Job: j, completed: completed > 0, failed: failed > 0,
	}
}
