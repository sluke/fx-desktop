package app

import "github.com/egoist/mygo"

func requestDirectoryDialog(options mygo.OpenDialogOptions,
	open func(mygo.OpenDialogOptions) ([]string, error),
	dispatch func(func()), complete func([]string, error),
) {
	// Dialog.Open marshals creation of the native panel to the UI thread.
	// Only its synchronous wait lives here, leaving AppKit's regular event
	// loop free to validate the selected directory and enable Open.
	go func() {
		paths, err := open(options)
		dispatch(func() { complete(paths, err) })
	}()
}
