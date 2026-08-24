package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// This file holds the pieces editors drive: a tmux popup wrapper, and a
// command that renders documentation to a Markdown file and prints its path.

// popup re-runs godocs inside a tmux popup so that an editor keybinding can
// show an interactive picker. Helix's :sh gives a command no terminal of its
// own, but tmux display-popup talks to the server directly.
func popup(args []string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if os.Getenv("TMUX") == "" {
		return runInherit(self, args...)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return runInherit(self, args...)
	}

	popupArgs := []string{"display-popup", "-E"}
	// An editor's :sh runs us detached from any tmux client, so "current
	// client" is undefined; target the inherited pane and let tmux find it.
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		popupArgs = append(popupArgs, "-t", pane)
	}
	// A popup gets a fresh environment, so forward what has to survive.
	for _, name := range []string{"GODOCS_PICK_OUT", "GODOCS_PICK_VERB", "GODOCS_GO", "GODOCS_QUIET"} {
		if v := os.Getenv(name); v != "" {
			popupArgs = append(popupArgs, "-e", name+"="+v)
		}
	}
	popupArgs = append(popupArgs,
		"-w", envOr("GODOCS_POPUP_WIDTH", "90%"),
		"-h", envOr("GODOCS_POPUP_HEIGHT", "85%"),
		"-d", "#{pane_current_path}",
		"-T", " go doc ",
		shellQuote(append([]string{self}, args...)),
	)
	return runInherit("tmux", popupArgs...)
}

// bufferDir holds the rendered Markdown files editors open. Naming each file
// after its symbol keeps buffer names meaningful and lets a repeat lookup reuse
// the same buffer.
func bufferDir() string {
	return filepath.Join(os.TempDir(), "godocs-buffers")
}

func bufferPath(name string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.' || r == '-' || r == '_':
			return r
		case r == '/':
			return '.'
		}
		return '_'
	}, name)
	if slug == "" {
		slug = "godocs"
	}
	return filepath.Join(bufferDir(), slug+".md")
}

func renderToBuffer(pkg, symbol string) (string, error) {
	if err := os.MkdirAll(bufferDir(), 0o755); err != nil {
		return "", err
	}
	name := pkg
	if symbol != "" {
		name += "." + symbol
	}
	out := bufferPath(name)
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := render(f, pkg, symbol, "md"); err != nil {
		return "", err
	}
	return out, nil
}

// pickToBuffer shows the picker in a popup and renders whatever comes back.
//
// The popup is a separate process with its own stdout, so the choice comes back
// through a file rather than a pipe.
func pickToBuffer(seed string, stdLibOnly bool) (string, error) {
	tmp, err := os.CreateTemp("", "godocs-pick-*")
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	args := []string{"pick"}
	if stdLibOnly {
		args = append(args, "--std")
	}
	if seed != "" {
		args = append(args, seed)
	}

	os.Setenv("GODOCS_PICK_OUT", tmp.Name())
	os.Setenv("GODOCS_PICK_VERB", "open in editor")
	defer os.Unsetenv("GODOCS_PICK_OUT")
	defer os.Unsetenv("GODOCS_PICK_VERB")

	if err := popup(args); err != nil {
		return "", errPickCancelled
	}

	data, err := os.ReadFile(tmp.Name())
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return "", errPickCancelled
	}
	fields := strings.Split(strings.TrimRight(string(data), "\n"), "\t")
	if fields[0] == "" {
		return "", errPickCancelled
	}
	symbol := ""
	if len(fields) > 1 {
		symbol = fields[1]
	}
	return renderToBuffer(fields[0], symbol)
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// shellQuote joins argv into one string safe for tmux to hand to a shell.
func shellQuote(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

func runInherit(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func clipboardCommand() string {
	if runtime.GOOS == "darwin" {
		return "pbcopy"
	}
	if _, err := exec.LookPath("wl-copy"); err == nil {
		return "wl-copy"
	}
	return "xclip -selection clipboard"
}

func openURL(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return runInherit("open", url)
	case "windows":
		return runInherit("cmd", "/c", "start", url)
	default:
		return runInherit("xdg-open", url)
	}
}

func pkgsiteURL(pkg, anchor string) string {
	base := envOr("GODOCS_PKGSITE", "https://pkg.go.dev")
	if anchor == "" {
		return fmt.Sprintf("%s/%s", base, pkg)
	}
	return fmt.Sprintf("%s/%s#%s", base, pkg, anchor)
}
