package app

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// The directory sheet must let AppKit's normal event loop keep running.
// Waiting on Dialog.Open on the caller's UI thread left Open disabled even
// with an existing directory selected. Results still belong on that thread.
func TestDirectoryDialogDoesNotBlockCallerAndDispatchesResult(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		err   error
	}{{"selected", []string{"/test/workspace"}, nil}, {"cancelled", nil, nil}, {"error", nil, errors.New("dialog failed")}} {
		t.Run(tc.name, func(t *testing.T) {
			started, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
			queued := make(chan func(), 1)
			completed := false
			options := mygo.OpenDialogOptions{Title: "Open project folder", DefaultPath: "/test", Directory: true, CreateDirectories: true}
			go func() {
				requestDirectoryDialog(options, func(got mygo.OpenDialogOptions) ([]string, error) {
					if !reflect.DeepEqual(got, options) {
						t.Errorf("options lost: %+v", got)
					}
					close(started)
					<-release
					return tc.paths, tc.err
				}, func(fn func()) { queued <- fn }, func(paths []string, err error) {
					completed = true
					if !reflect.DeepEqual(paths, tc.paths) || err != tc.err {
						t.Errorf("result=(%v,%v), want (%v,%v)", paths, err, tc.paths, tc.err)
					}
				})
				close(returned)
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("dialog did not start")
			}
			select {
			case <-returned:
			case <-time.After(100 * time.Millisecond):
				t.Error("directory picker blocked its UI caller while awaiting selection")
			}
			close(release)
			select {
			case fn := <-queued:
				if completed {
					t.Fatal("dialog result ran before UI dispatcher")
				}
				fn()
				if !completed {
					t.Fatal("dispatched result did not complete")
				}
			case <-time.After(time.Second):
				t.Fatal("dialog result was not dispatched")
			}
			<-returned
		})
	}
}

func TestDirectoryDialogIgnoresDuplicateRequest(t *testing.T) {
	c := &Coordinator{directoryPicking: true}
	c.pickDirectoryOnMain(nil, "Open project folder", func(string) { t.Fatal("pending directory request was duplicated") })
	if !c.directoryPicking {
		t.Fatal("duplicate request cleared the pending selection")
	}
}
