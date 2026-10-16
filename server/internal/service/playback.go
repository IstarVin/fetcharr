package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/istarvin/fetcharr/server/internal/jobs"
)

// OpenMedia opens only a completed job's library file. os.Root prevents
// traversal and symlink escapes, including changes made while opening it.
// Use the saved destination so category edits do not break older jobs.
func (s *Service) OpenMedia(ctx context.Context, id string) (*os.File, error) {
	j, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if j.Status != jobs.StatusCompleted || j.FilePath == nil || *j.FilePath == "" {
		return nil, newErr(CodeInvalidState, nil, "only completed jobs with a media file can be played")
	}
	if j.DestinationRoot == "" {
		return nil, invalid("original destination of this job is unknown; cannot play the file")
	}
	rel, err := filepath.Rel(j.DestinationRoot, *j.FilePath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, invalid("the file is outside its original category; cannot play the file")
	}
	root, err := os.OpenRoot(j.DestinationRoot)
	if err != nil {
		return nil, newErr(CodeNotFound, err, "media file is unavailable")
	}
	defer func() { _ = root.Close() }()
	// Refuse directories and special files before opening (a FIFO could block).
	info, err := root.Stat(rel)
	if err != nil || !info.Mode().IsRegular() {
		return nil, newErr(CodeNotFound, err, "media file is unavailable")
	}
	f, err := root.Open(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			return nil, newErr(CodeNotFound, err, "media file is unavailable")
		}
		return nil, fmt.Errorf("open media for job %s: %w", id, err)
	}
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, newErr(CodeNotFound, err, "media file is unavailable")
	}
	return f, nil
}
