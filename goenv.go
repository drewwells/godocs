package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Locating the Go toolchain is not as simple as exec.LookPath. Raycast, launchd
// and editor subprocesses start with a bare PATH, and version managers like
// mise activate from ~/.zshrc, which a login shell does not read. So probe the
// cheap sources in order and remember the answer on disk.

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

func findGo() (string, error) {
	if p := os.Getenv("GODOCS_GO"); p != "" {
		return p, nil
	}
	if p, err := exec.LookPath("go"); err == nil {
		return p, nil
	}

	home, _ := os.UserHomeDir()
	cache := filepath.Join(cacheDir(), "go-path")

	// A version manager knows better than any hardcoded path.
	if mise := filepath.Join(home, ".local", "bin", "mise"); isExecutable(mise) {
		if out, err := exec.Command(mise, "which", "go").Output(); err == nil {
			if p := strings.TrimSpace(string(out)); isExecutable(p) {
				rememberGo(cache, p)
				return p, nil
			}
		}
	}

	if data, err := os.ReadFile(cache); err == nil {
		if p := strings.TrimSpace(string(data)); isExecutable(p) {
			return p, nil
		}
	}

	for _, p := range []string{
		"/opt/homebrew/bin/go",
		"/usr/local/go/bin/go",
		"/usr/local/bin/go",
		filepath.Join(home, "go", "bin", "go"),
	} {
		if isExecutable(p) {
			rememberGo(cache, p)
			return p, nil
		}
	}

	// Last resort: an interactive shell, which does source ~/.zshrc.
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	if out, err := exec.Command(shell, "-ic", "command -v go").Output(); err == nil {
		lines := strings.Fields(strings.TrimSpace(string(out)))
		if n := len(lines); n > 0 && isExecutable(lines[n-1]) {
			rememberGo(cache, lines[n-1])
			return lines[n-1], nil
		}
	}

	return "", errNoGo
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
