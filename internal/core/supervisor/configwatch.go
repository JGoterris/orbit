package supervisor

import (
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// watchDirs calls onChange when any of the files changes, until stop is closed. The directories are registered
// before it returns, so an edit made right after is not missed.
func watchDirs(files []string, stop <-chan struct{}, onChange func()) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	names := map[string]bool{}
	dirs := map[string]bool{}
	for _, f := range files {
		names[filepath.Clean(f)] = true
		dirs[filepath.Dir(f)] = true
	}
	for d := range dirs {
		w.Add(d)
	}
	go func() {
		defer w.Close()
		for {
			select {
			case <-stop:
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if names[filepath.Clean(ev.Name)] {
					onChange()
				}
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()
}
