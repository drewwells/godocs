package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// This file holds the pieces editors drive: a tmux popup wrapper, and a
// command that renders documentation to a Markdown file and prints its path.

// errNoTerminal means there is nowhere safe to draw an interactive picker.
var errNoTerminal = errors.New("no terminal available for the picker")

// hasTerminal reports whether our own output is a terminal we may draw on.
func hasTerminal() bool {
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return true
		}
	}
	return false
}

// popup re-runs godocs inside a tmux popup so that an editor keybinding can
// show an interactive picker. Helix's :sh gives a command no terminal of its
// own, but tmux display-popup talks to the server directly.
//
// Outside tmux there is nowhere safe to draw. Running the picker anyway is
// actively harmful: fzf opens /dev/tty itself, so it would paint over the
// editor that invoked us — the editor keeps its own idea of the screen and the
// display is left corrupted. Refuse instead, and let the caller degrade.
func popup(args []string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if os.Getenv("TMUX") == "" || !haveTmux() {
		if hasTerminal() {
			return runInherit(self, args...)
		}
		return errNoTerminal
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
		if errors.Is(err, errNoTerminal) {
			// No picker is possible here, but the query is still worth
			// answering: hand back the matches as a buffer.
			return matchesToBuffer(seed)
		}
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

func haveTmux() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
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

// matchesBufferLimit keeps the fallback list scannable rather than exhaustive.
const matchesBufferLimit = 40

// matchesToBuffer writes the search results as a Markdown buffer, for when no
// picker can be shown.
//
// The list is actionable rather than a dead end: the editor binding that looks
// up the word under the cursor works in any buffer, including this one, so each
// name here is one keystroke away from its documentation.
func matchesToBuffer(query string) (string, error) {
	files, err := indexFiles(false)
	if err != nil {
		return "", err
	}
	entries, err := searchIndex(files, query, "", matchesBufferLimit, false)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(bufferDir(), 0o755); err != nil {
		return "", err
	}
	name := query
	if name == "" {
		name = "matches"
	}
	out := bufferPath("godocs-" + name)

	var b strings.Builder
	if query == "" {
		b.WriteString("# godocs\n\n")
	} else {
		fmt.Fprintf(&b, "# godocs: %q\n\n", query)
	}

	if len(entries) == 0 {
		fmt.Fprintf(&b, "No matches.\n\nSearch again with `godocs %s` in a terminal.\n", query)
	} else {
		b.WriteString("Put the cursor on a name below and press the lookup key")
		b.WriteString(" (`+D` by default) to open its documentation.\n\n")
		for _, e := range entries {
			fmt.Fprintf(&b, "- `%s` — %s\n", e.fields[1], e.fields[5])
			if e.fields[6] != "" {
				fmt.Fprintf(&b, "  %s\n", e.fields[6])
			}
		}
		b.WriteString("\n---\n\n")
	}

	// Say why this is a list and not the picker, so it does not read as a bug.
	b.WriteString("_The interactive picker needs tmux: it runs in a tmux popup so it has a\n")
	b.WriteString("terminal of its own to draw on. Without one it would paint over the editor._\n")

	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return out, nil
}
