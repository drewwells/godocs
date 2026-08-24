package main

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// indexFormat is bumped whenever the TSV layout changes, so that upgrading
// godocs rebuilds stale indexes instead of misreading them.
const indexFormat = "v1"

var errNoGo = errors.New("no working Go toolchain found; install Go, or point GODOCS_GO at the go binary")

func cacheDir() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return filepath.Join(d, "godocs")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "godocs")
	}
	return filepath.Join(home, ".cache", "godocs")
}

// stdIndexPath must stay subprocess-free on the hot path: the picker re-runs a
// search on every keystroke, and `go env GOVERSION` alone costs ~80ms. Any
// cached index is authoritative; only a build consults the toolchain.
func stdIndexPath() string {
	matches, _ := filepath.Glob(filepath.Join(cacheDir(), "std-*-"+indexFormat+".tsv"))
	if len(matches) == 1 {
		if fi, err := os.Stat(matches[0]); err == nil && fi.Size() > 0 {
			return matches[0]
		}
	}
	return stdIndexPathFor(goVersion())
}

func stdIndexPathFor(version string) string {
	return filepath.Join(cacheDir(), fmt.Sprintf("std-%s-%s.tsv", version, indexFormat))
}

// depsIndexPath names a per-module index, keyed by the path of its go.mod.
func depsIndexPath() (string, bool) {
	gomod, ok := moduleRoot()
	if !ok {
		return "", false
	}
	sum := sha1.Sum([]byte(gomod))
	key := hex.EncodeToString(sum[:])[:12]
	return filepath.Join(cacheDir(), fmt.Sprintf("deps-%s-%s.tsv", key, indexFormat)), true
}

// anyDepsIndex reports whether any module has ever been indexed, so the common
// case skips looking for a go.mod at all.
func anyDepsIndex() bool {
	matches, _ := filepath.Glob(filepath.Join(cacheDir(), "deps-*.tsv"))
	return len(matches) > 0
}

func buildStdIndex(force bool) (string, error) {
	if !force {
		if p := stdIndexPath(); fileHasContent(p) {
			return p, nil
		}
	}
	out := stdIndexPathFor(goVersion())
	warnf("indexing the standard library (%s)", goVersion())
	if err := writeIndex(out, []string{"std"}, stdOnly); err != nil {
		return "", err
	}
	// Drop indexes built against other Go versions or older formats.
	matches, _ := filepath.Glob(filepath.Join(cacheDir(), "std-*.tsv"))
	for _, m := range matches {
		if m != out {
			_ = os.Remove(m)
		}
	}
	return out, nil
}

func buildDepsIndex(force bool) (string, error) {
	gomod, ok := moduleRoot()
	if !ok {
		return "", errors.New("not inside a Go module")
	}
	out, _ := depsIndexPath()
	if !force && fileHasContent(out) && newerThan(out, gomod) {
		return out, nil
	}
	warnf("indexing dependencies of %s", filepath.Dir(gomod))
	if err := writeIndex(out, []string{"-deps", "./..."}, nonStdOnly); err != nil {
		return "", err
	}
	return out, nil
}

func writeIndex(out string, patterns []string, filter pkgFilter) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := buildIndex(f, patterns, filter); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, out)
}

// indexFiles lists the indexes a search should span, standard library first.
func indexFiles(stdLibOnly bool) ([]string, error) {
	std, err := buildStdIndex(false)
	if err != nil {
		return nil, err
	}
	files := []string{std}
	if stdLibOnly {
		return files, nil
	}
	if anyDepsIndex() {
		if deps, ok := depsIndexPath(); ok && fileHasContent(deps) {
			files = append(files, deps)
		}
	}
	return files, nil
}

func fileHasContent(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Size() > 0
}

func newerThan(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return true
	}
	return fa.ModTime().After(fb.ModTime())
}

func warnf(format string, args ...any) {
	if os.Getenv("GODOCS_QUIET") != "" {
		return
	}
	fmt.Fprintf(os.Stderr, "godocs: "+format+"\n", args...)
}
