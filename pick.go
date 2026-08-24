package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// pickResult is what the picker hands back: enough to render or link a symbol.
type pickResult struct {
	Pkg    string
	Anchor string
}

var errPickCancelled = errors.New("nothing selected")

// pickLimit caps how many rows a single keystroke redraws. Any real query
// narrows well below this; it only bounds the cost of a near-empty one.
const pickLimit = 300

// pickRows prints one ranked page of picker rows for the current query. fzf
// calls this back on every keystroke, so it has to stay in the tens of
// milliseconds.
func pickRows(query string, stdLibOnly bool) error {
	files, err := indexFiles(stdLibOnly)
	if err != nil {
		return err
	}
	entries, err := searchIndex(files, query, "", pickLimit, false)
	if err != nil {
		return err
	}

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	for _, e := range entries {
		label := fmt.Sprintf("%-34s  %-58s", e.fields[1], e.fields[5])
		if e.fields[6] != "" {
			label += "  " + e.fields[6]
		}
		// Short package names collide (text/template vs html/template), so keep
		// the import path visible whenever it is not just the short name.
		if e.kind != "pkg" && strings.Contains(e.pkg, "/") {
			label += "  <" + e.pkg + ">"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", label, e.fields[3], e.fields[4])
	}
	return nil
}

// pick runs fzf over the index and returns what was chosen.
//
// fzf does no filtering of its own (--disabled): every keystroke re-runs our
// ranked search, so the picker and the Raycast extension order results the same
// way, and "wtgrp" still finds sync.WaitGroup.
func pick(query, enterVerb string, stdLibOnly bool) (pickResult, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return pickResult{}, errors.New("fzf is required for the interactive picker")
	}
	self, err := os.Executable()
	if err != nil {
		return pickResult{}, err
	}

	stdFlag := ""
	if stdLibOnly {
		stdFlag = " --std"
	}
	if enterVerb == "" {
		enterVerb = "read"
	}

	args := []string{
		"--disabled",
		"--delimiter=\t",
		"--with-nth=1",
		"--query=" + query,
		"--prompt=go doc > ",
		"--info=inline",
		"--header=enter: " + enterVerb + "  ·  ctrl-o: pkg.go.dev  ·  ctrl-y: copy import path  ·  ctrl-/: preview layout",
		"--bind=start:reload(" + self + " _rows" + stdFlag + " {q})",
		"--bind=change:reload(" + self + " _rows" + stdFlag + " {q})",
		"--preview=" + self + " render {2} {3} --format text",
		"--preview-window=down,65%,border-top,wrap",
		"--bind=ctrl-o:execute-silent(" + self + " open-url {2} {3})",
		"--bind=ctrl-y:execute-silent(printf %s {2} | " + clipboardCommand() + ")",
		"--bind=ctrl-/:change-preview-window(right,55%|hidden|down,65%)",
	}

	cmd := exec.Command("fzf", args...)
	cmd.Stdin, cmd.Stderr = devNull(), os.Stderr
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return pickResult{}, errPickCancelled
	}

	fields := strings.Split(strings.TrimRight(string(out), "\n"), "\t")
	if len(fields) < 2 || fields[1] == "" {
		return pickResult{}, errPickCancelled
	}
	res := pickResult{Pkg: fields[1]}
	if len(fields) > 2 {
		res.Anchor = fields[2]
	}
	return res, nil
}

func devNull() *os.File {
	f, err := os.Open(os.DevNull)
	if err != nil {
		return nil
	}
	return f
}
