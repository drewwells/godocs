package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Locating the Go toolchain is not as simple as exec.LookPath. Raycast, launchd
// and editor subprocesses start with a bare PATH and an unrelated working
// directory, and version managers install shims that only resolve where their
// config applies. So gather candidates, and prove each one works before
// trusting it.

var (
	goOnce sync.Once
	goPath string
	goErr  error
)

func goBinary() (string, error) {
	goOnce.Do(func() {
		goPath, goErr = findGo()
	})
	return goPath, goErr
}

// goWorks reports whether a candidate is a usable toolchain.
//
// The probe deliberately runs in a neutral directory. A version-manager shim
// resolves against the config it finds by walking up from the working
// directory, so one can work in your home directory and fail from "/" — and
// the answer we cache has to work everywhere, because Raycast and launchd run
// from somewhere else entirely.
func goWorks(path string) bool {
	if !isExecutable(path) {
		return false
	}
	cmd := exec.Command(path, "env", "GOVERSION")
	cmd.Dir = os.TempDir()
	out, err := cmd.Output()
	return err == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "go")
}

func findGo() (string, error) {
	cache := filepath.Join(cacheDir(), "go-path")

	// An explicit override is the user's business; report it plainly if broken
	// rather than silently using something else.
	if p := os.Getenv("GODOCS_GO"); p != "" {
		if goWorks(p) {
			return p, nil
		}
		return "", errNoGo
	}

	// A previously validated path is an absolute one, so it costs nothing to
	// re-check that it is still there.
	if data, err := os.ReadFile(cache); err == nil {
		if p := strings.TrimSpace(string(data)); isExecutable(p) {
			return p, nil
		}
	}

	for _, candidate := range goCandidates() {
		if goWorks(candidate) {
			rememberGo(cache, candidate)
			return candidate, nil
		}
	}
	return "", errNoGo
}

// goCandidates lists places a Go toolchain might be, cheapest first. Shims come
// before concrete installs because they are usually right; goWorks is what
// catches the case where they are not.
func goCandidates() []string {
	var candidates []string
	add := func(paths ...string) {
		for _, p := range paths {
			if p != "" {
				candidates = append(candidates, p)
			}
		}
	}

	if p, err := exec.LookPath("go"); err == nil {
		add(p)
	}

	home, _ := os.UserHomeDir()
	mise := filepath.Join(home, ".local", "bin", "mise")
	if isExecutable(mise) {
		if out, err := exec.Command(mise, "which", "go").Output(); err == nil {
			add(strings.TrimSpace(string(out)))
		}
	}

	// Version managers keep concrete installs alongside their shims. These
	// paths carry no config dependency, so they work from any directory.
	add(versionManagerInstalls(home)...)

	add(
		"/opt/homebrew/bin/go",
		"/opt/homebrew/opt/go/bin/go",
		"/usr/local/go/bin/go",
		"/usr/local/bin/go",
		filepath.Join(home, "go", "bin", "go"),
	)

	// Last resort: an interactive shell, which sources the rc file where tools
	// like mise are usually activated.
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	if out, err := exec.Command(shell, "-ic", "command -v go").Output(); err == nil {
		fields := strings.Fields(strings.TrimSpace(string(out)))
		if n := len(fields); n > 0 {
			add(fields[n-1])
		}
	}

	return candidates
}

// versionManagerInstalls finds concrete toolchains under mise and asdf,
// newest-looking first.
func versionManagerInstalls(home string) []string {
	patterns := []string{
		filepath.Join(home, ".local", "share", "mise", "installs", "go", "*", "bin", "go"),
		filepath.Join(home, ".asdf", "installs", "golang", "*", "go", "bin", "go"),
	}

	var found []string
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		// Prefer fully specified versions (1.26.4) over the aliases that sit
		// beside them (1.26, 1, latest): the aliases move, and a cached path
		// should stay valid until the toolchain it names is actually removed.
		sort.Slice(matches, func(i, j int) bool {
			si, sj := specificity(matches[i]), specificity(matches[j])
			if si != sj {
				return si > sj
			}
			return matches[i] > matches[j]
		})
		found = append(found, matches...)
	}
	return found
}

// specificity counts the dots in the version component of an install path, so
// that "1.26.4" outranks "1.26", which outranks "1" and "latest".
func specificity(path string) int {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, part := range parts {
		if (part == "go" || part == "golang") && i+1 < len(parts) {
			return strings.Count(parts[i+1], ".")
		}
	}
	return 0
}

func rememberGo(path, value string) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(value+"\n"), 0o644)
}

func isExecutable(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// goCommand builds a `go` invocation with the toolchain's own directory on
// PATH, so anything it shells out to in turn is also reachable.
func goCommand(args ...string) (*exec.Cmd, error) {
	bin, err := goBinary()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	return cmd, nil
}

var goVersionOnce sync.Once
var goVersionValue string

// goVersion keys the standard library index, so that upgrading Go rebuilds it.
func goVersion() string {
	goVersionOnce.Do(func() {
		goVersionValue = "unknown"
		cmd, err := goCommand("env", "GOVERSION")
		if err != nil {
			return
		}
		if out, err := cmd.Output(); err == nil {
			if v := strings.TrimSpace(string(out)); v != "" {
				goVersionValue = v
			}
		}
	})
	return goVersionValue
}

// moduleRoot walks up for a go.mod rather than shelling out to `go env GOMOD`,
// which would put an ~80ms subprocess on every search once any module has been
// indexed.
func moduleRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		candidate := filepath.Join(dir, "go.mod")
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
