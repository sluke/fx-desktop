package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/testutil"
)

// A mention is read where the message is sent, and the content comes from the
// file: what fx is given is the file's own bytes and the path it is really at.
func TestResolveMentionsReadsTheFilesAMessageNames(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main\n")
	write("internal/app/console.go", "package app\n")

	mentions, err := resolveMentions(dir, "look at @main.go and @internal/app/console.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(mentions) != 2 {
		t.Fatalf("mentions = %+v", mentions)
	}
	if mentions[0].Path != "main.go" || mentions[0].Text != "package main\n" || mentions[0].Abs != filepath.Join(dir, "main.go") {
		t.Fatalf("first mention = %+v", mentions[0])
	}
	if mentions[1].Path != "internal/app/console.go" || mentions[1].Text != "package app\n" {
		t.Fatalf("second mention = %+v", mentions[1])
	}
	// A message with no mention reads no file at all.
	if mentions, err := resolveMentions(dir, "look at the whole project"); err != nil || len(mentions) != 0 {
		t.Fatalf("a plain message = %+v, %v", mentions, err)
	}
}

// A token that names nothing is prose, not a mistake: the picker only writes
// paths that exist, and a message about someone else's file is not an error.
func TestResolveMentionsPassesOverWhatIsNotAMention(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Dir(dir)
	if err := os.WriteFile(filepath.Join(parent, "outside.go"), []byte("package outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"look at @nope.go",
		"look at @../outside.go", // outside the project: not a file of ours
		"look at @/etc/hosts",    // absolute: not a file of ours
		"look at @dir",           // a directory is not a file to read
	} {
		mentions, err := resolveMentions(dir, text)
		if err != nil || len(mentions) != 0 {
			t.Errorf("resolveMentions(%q) = %+v, %v; want nothing and no refusal", text, mentions, err)
		}
	}
}

// What cannot be attached refuses the message, with the reason, rather than
// arriving as a mention that carries a fraction of a file. The limits are the
// sizes fx itself holds a project instruction file to.
func TestResolveMentionsRefusesWhatFxCannotTake(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string, body []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	filler := bytes.Repeat([]byte("x"), maxMentionBytes)
	write("exactly.txt", filler)
	write("toobig.txt", append(filler, 'x'))
	write("binary.bin", []byte{0xff, 0xfe, 0x00, 0x01})

	// The limit is the limit: a file of exactly the size fx takes is taken.
	mentions, err := resolveMentions(dir, "read @exactly.txt")
	if err != nil || len(mentions) != 1 {
		t.Fatalf("a file of exactly the limit = %+v, %v", mentions, err)
	}
	mentions, err = resolveMentions(dir, "read @toobig.txt")
	if len(mentions) != 0 || err == nil || !strings.Contains(err.Error(), "toobig.txt") || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("an oversized file = %+v, %v", mentions, err)
	}
	mentions, err = resolveMentions(dir, "read @binary.bin")
	if len(mentions) != 0 || err == nil || !strings.Contains(err.Error(), "not a text file") {
		t.Fatalf("a binary file = %+v, %v", mentions, err)
	}
	// Together they have a limit too: two files of the per-file size fit a
	// message, a third does not.
	write("second.txt", filler)
	write("third.txt", filler)
	mentions, err = resolveMentions(dir, "read @exactly.txt and @second.txt")
	if err != nil || len(mentions) != 2 {
		t.Fatalf("two files of the per-file size = %+v, %v", mentions, err)
	}
	mentions, err = resolveMentions(dir, "read @exactly.txt @second.txt @third.txt")
	if len(mentions) != 0 || err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("a message over the total = %+v, %v", mentions, err)
	}
	// A refusal holds the whole message: nothing half-attached goes out.
	if mentions != nil {
		t.Fatalf("a refused message still carried %+v", mentions)
	}
}

// The picker offers the project's files — what git tracks and what it does not
// ignore — so the list is the project rather than everything on disk.
func TestWorkspaceFilesListsTheProject(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init")
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main\n")
	write("internal/app/console.go", "package app\n")
	write("untracked.go", "package main\n")
	write("ignored.go", "package main\n")
	write(".gitignore", "ignored.go\n")
	runGit(t, dir, "add", "main.go", "internal/app/console.go", ".gitignore")

	files, err := workspaceFiles(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	rows := make(map[string]chrome.MentionFile, len(files))
	for _, file := range files {
		rows[file.Path] = file
	}
	for _, want := range []string{"main.go", "internal/app/console.go", "untracked.go", ".gitignore"} {
		if _, ok := rows[want]; !ok {
			t.Fatalf("%s is not offered: %+v", want, files)
		}
	}
	if _, ok := rows["ignored.go"]; ok {
		t.Fatalf("an ignored file was offered: %+v", files)
	}
	if row := rows["internal/app/console.go"]; row.Name != "console.go" || row.Dir != "internal/app" {
		t.Fatalf("row = %+v", row)
	}
	if row := rows["main.go"]; row.Name != "main.go" || row.Dir != "" {
		t.Fatalf("a file at the top of the project = %+v", row)
	}
}

// awaitMentionFiles waits for the read the picker asked for, and reports what
// it found — or why the project could not be listed.
func awaitMentionFiles(t *testing.T, c *Coordinator, s *ProjectWindow) ([]chrome.MentionFile, string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		files, loading, problem := c.sessionMentionFiles(s)
		if !loading {
			return files, problem
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the project's files were never read")
	return nil, ""
}

func TestOpenMentionsReadsTheProjectForThePicker(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	coord := NewCoordinator()
	s := &ProjectWindow{coord: coord, project: dir}
	coord.openMentions(s)
	files, problem := awaitMentionFiles(t, coord, s)
	if problem != "" || len(files) != 1 || files[0].Path != "main.go" {
		t.Fatalf("files = %+v, %q", files, problem)
	}
	// A project that cannot be listed says so, rather than reading forever.
	missing := &ProjectWindow{coord: coord, project: filepath.Join(dir, "gone")}
	coord.openMentions(missing)
	if files, problem := awaitMentionFiles(t, coord, missing); problem == "" || len(files) != 0 {
		t.Fatalf("an unreadable project = %+v, %q", files, problem)
	}
	// A session with no project at all is not asked for anything.
	coord.openMentions(&ProjectWindow{coord: coord})
	if _, loading, _ := coord.sessionMentionFiles(&ProjectWindow{coord: coord}); loading {
		t.Fatal("a session with no project was left reading")
	}
}

// A mention rides the prompt as the one resource shape fx takes, with the
// file's own bytes. The fixture reads the block the way fx does — the uri
// inside the workspace, the text equal to the file — so a wrong shape or a
// wrong content fails here, not in front of a model.
func TestChatSendsMentionedFiles(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	path := filepath.Join(chat.project, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !chat.snapshot().Files {
		t.Fatal("fx advertises promptCapabilities.embeddedContext")
	}
	mentions, err := resolveMentions(chat.project, "look at @main.go please")
	if err != nil || len(mentions) != 1 {
		t.Fatalf("resolveMentions = %+v, %v", mentions, err)
	}
	if !chat.submit("look at @main.go please", nil, mentions) {
		t.Fatal("not submitted")
	}
	s := awaitChat(t, chat, func(s chatSnapshot) bool { return s.Permission != nil })
	if len(s.Messages) != 2 || s.Messages[0].Text != "look at @main.go please" {
		t.Fatalf("transcript: %+v", s.Messages)
	}
	chat.choosePermission(s.Permission.ID, "allow_once")
	s = awaitChat(t, chat, func(s chatSnapshot) bool { return !s.Busy })
	if s.Problem != "" {
		t.Fatalf("fx refused the message: %s", s.Problem)
	}
}

// A session whose fx takes no file content sends the message as the words it
// is: the composer offers no picker, and nothing claims to be attached.
func TestChatWithoutEmbeddedContextSendsText(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv("FX_DESKTOP_ACP_TEST_NO_EMBEDDED_CONTEXT", "1")
	chat := chatFixture(t)
	if err := os.WriteFile(filepath.Join(chat.project, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if chat.snapshot().Files {
		t.Fatal("this fixture does not advertise embeddedContext")
	}
	// The app reads no file, so no block is sent and fx takes the text.
	if !chat.submit("look at @main.go", nil, nil) {
		t.Fatal("not submitted")
	}
	s := awaitChat(t, chat, func(s chatSnapshot) bool { return s.Permission != nil })
	chat.choosePermission(s.Permission.ID, "allow_once")
	s = awaitChat(t, chat, func(s chatSnapshot) bool { return !s.Busy })
	if s.Problem != "" {
		t.Fatalf("fx refused a plain message: %s", s.Problem)
	}
}
