package app

import (
	"context"
	"time"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/workspace"
	workspaceread "fx-desktop/internal/workspace/read"
)

// The mention limits are the workspace's: they are fx's own sizes, not a
// presentation choice.
const (
	maxMentionBytes      = workspace.MaxMentionBytes
	maxMentionTotalBytes = workspace.MaxMentionTotalBytes
)

// promptMention is one file a message names with @path. The type belongs to
// the workspace package: reading a project file is a workspace fact, not an
// app or UI one.
type promptMention = workspace.Mention

// workspacePath resolves a mention to a file in the project.
func workspacePath(project, token string) (string, bool) {
	return workspace.ResolvePath(project, token)
}

// resolveMentions reads every file a message names with @path, without a
// cancellation scope. Callers that can be cancelled — the submit path, whose
// result may be discarded when the draft or the window changes — use
// resolveMentionsContext instead. This wrapper exists for tests and for the
// few call sites that are already off the UI thread with no scope of their
// own.
func resolveMentions(project, text string) ([]promptMention, error) {
	return resolveMentionsContext(context.Background(), project, text)
}

// resolveMentionsContext reads the files a message names, stopping between
// files when ctx is cancelled. The read is bounded, so a file that grows after
// its size was checked is refused rather than attached truncated. A
// *workspaceread.Refusal is a message that cannot be sent as written; a
// cancellation is not, and the caller must drop the submission instead of
// reporting it as a problem with the draft (§B3).
func resolveMentionsContext(ctx context.Context, project, text string) ([]promptMention, error) {
	return workspaceread.Resolve(ctx, project, text)
}

// workspaceFiles lists the files the @ picker offers.
func workspaceFiles(ctx context.Context, dir string) ([]chrome.MentionFile, error) {
	return workspaceread.ListFiles(ctx, dir)
}

// openMentions reads the project's files for the @ picker. It runs off the UI
// thread and repaints: the picker opens on a keystroke, and listing a large
// project must not stall the message being written. Each opening rereads, so a
// file added a moment ago is offered.
func (c *Coordinator) openMentions(s *ProjectWindow) {
	if s == nil || s.project == "" {
		return
	}
	s.workspace.mu.Lock()
	if s.workspace.mentionLoading {
		s.workspace.mu.Unlock()
		return
	}
	// Bumping the workspace generation before the read is published means a
	// slower, older listing cannot overwrite a newer one.
	s.workspace.gen++
	generation := s.workspace.gen
	s.workspace.mentionLoading, s.workspace.mentionErr = true, nil
	project := s.project
	s.workspace.mu.Unlock()
	s.invalidate()
	scope, stop := s.scope("mentions")
	go func() {
		defer stop()
		files, err := workspaceFiles(scope, project)
		s.dispatch(func() {
			s.workspace.mu.Lock()
			if s.workspace.gen != generation {
				s.workspace.mu.Unlock()
				return
			}
			s.workspace.mentionLoading, s.workspace.mentionErr = false, err
			if err == nil {
				s.workspace.mentionFiles = files
			}
			s.workspace.mu.Unlock()
			s.invalidate()
		})
	}()
}

// sessionMentionFiles projects the file list into the picker. It reads only:
// the render pass must not start a git call of its own.
func (c *Coordinator) sessionMentionFiles(s *ProjectWindow) ([]chrome.MentionFile, bool, string) {
	s.workspace.mu.Lock()
	defer s.workspace.mu.Unlock()
	problem := ""
	if s.workspace.mentionErr != nil {
		problem = "This project's files could not be listed"
	}
	return s.workspace.mentionFiles, s.workspace.mentionLoading, problem
}

// mentionTimeout bounds one submit's file preparation so a hung filesystem
// cannot hold the composer forever.
const mentionTimeout = 30 * time.Second
