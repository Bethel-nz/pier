package runner

import (
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// skipDirs are never walked unless a pattern names them: they are large, and
// tools rewrite them constantly.
var skipDirs = map[string]bool{".git": true, "node_modules": true, ".pier": true}

type stamp struct {
	mod  time.Time
	size int64
}

// watcher polls the files under root that match its globs. Polling costs a
// directory walk per tick but has no limits to raise and no editor quirks.
type watcher struct {
	root     string
	patterns []string
	files    map[string]stamp
}

func newWatcher(root string, patterns []string) *watcher {
	w := &watcher{root: root, patterns: patterns}
	w.files = w.scan()
	return w
}

// changed rescans and returns one file that was added, removed, or modified
// since the last scan, or "".
func (w *watcher) changed() string {
	next := w.scan()
	defer func() { w.files = next }()
	for name, now := range next {
		if before, ok := w.files[name]; !ok || before != now {
			return name
		}
	}
	for name := range w.files {
		if _, ok := next[name]; !ok {
			return name
		}
	}
	return ""
}

func (w *watcher) scan() map[string]stamp {
	files := map[string]stamp{}
	_ = filepath.WalkDir(w.root, func(full string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(w.root, full)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel != "." && skipDirs[entry.Name()] && !w.named(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !w.matches(rel) {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			files[rel] = stamp{mod: info.ModTime(), size: info.Size()}
		}
		return nil
	})
	return files
}

func (w *watcher) matches(rel string) bool {
	for _, pattern := range w.patterns {
		if matchGlob(pattern, rel) {
			return true
		}
	}
	return false
}

// named reports whether a pattern points inside dir by name, as in
// "node_modules/my-lib/**".
func (w *watcher) named(dir string) bool {
	for _, pattern := range w.patterns {
		if strings.HasPrefix(pattern, dir+"/") {
			return true
		}
	}
	return false
}

// matchGlob matches a slash-separated path against a glob where ** spans any
// number of folders, including none.
func matchGlob(pattern, name string) bool {
	return matchParts(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchParts(pattern, name []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchParts(pattern[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pattern[0], name[0]); !ok {
			return false
		}
		pattern, name = pattern[1:], name[1:]
	}
	return len(name) == 0
}
