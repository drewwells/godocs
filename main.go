// Command godocs is a fast, offline, fuzzy lookup for Go documentation.
//
// It indexes every package and exported symbol in the standard library — and,
// on demand, the current module's dependencies — into a flat TSV, then ranks
// queries against it. One index backs the terminal picker, editor integration
// and the Raycast extension, so they all agree on what "marshal" means.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

const usage = `godocs - fast fuzzy lookup of Go documentation, offline.

Usage:
  godocs [query...]              Interactive picker, optionally seeded
  godocs search <query>          Ranked matches as TSV, for scripts
  godocs doc <target>            Render docs for e.g. net/http.Client.Do
  godocs render <pkg> [symbol]   Render docs, package and symbol pre-split
  godocs buffer [text]           Render to a file and print its path, for editors
  godocs popup [args...]         Run godocs in a tmux popup
  godocs url <target>            Print the pkg.go.dev URL
  godocs open <target>           Open pkg.go.dev in a browser
  godocs resolve <target>        Print "<import path>\t<symbol>"
  godocs index [--force]         Build or refresh the standard library index
  godocs deps [--force]          Index the current module's dependencies
  godocs where                   Print cache paths

Search options:
  --limit N       Cap results (default 50)
  --kind K        Restrict to pkg|func|type|method|const|var
  --std           Ignore the dependency index even when one exists
  --names-only    Do not fall back to matching package synopses

Render options:
  --format md|text   Output format (default text for a terminal, md otherwise)

Buffer options:
  --pick             Always show the picker, even if the text resolves
  --fallback <path>  Print <path> if the picker is cancelled

Environment:
  GODOCS_GO         Path to the go binary, if it cannot be found automatically
  GODOCS_PKGSITE    Base URL for documentation links (default https://pkg.go.dev)
  GODOCS_PICK_OUT   File for the picker to write its choice to, instead of
                    rendering it
`

// options are the flags shared across subcommands.
type options struct {
	kind      string
	format    string
	fallback  string
	limit     int
	force     bool
	stdOnly   bool
	namesOnly bool
	pick      bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, errPickCancelled) {
			return
		}
		fmt.Fprintln(os.Stderr, "godocs:", err)
		os.Exit(1)
	}
}

var commands = map[string]bool{
	"pick": true, "search": true, "doc": true, "show": true, "render": true,
	"buffer": true, "popup": true, "url": true, "open": true, "open-url": true,
	"resolve": true, "clean": true, "index": true, "deps": true, "where": true,
	"_rows": true, "help": true,
}

func run(argv []string) error {
	cmd := "pick"
	if len(argv) > 0 {
		switch {
		case argv[0] == "-h" || argv[0] == "--help":
			fmt.Print(usage)
			return nil
		case commands[argv[0]]:
			cmd, argv = argv[0], argv[1:]
		}
		// Anything else is a bare query for the picker: `godocs marshal json`.
	}

	opts, args := parseOptions(argv)

	switch cmd {
	case "help":
		fmt.Print(usage)
		return nil

	case "pick":
		res, err := pick(strings.Join(args, " "), os.Getenv("GODOCS_PICK_VERB"), opts.stdOnly)
		if err != nil {
			return err
		}
		// A caller can ask for the choice rather than the rendering — an editor
		// about to open the docs as a buffer.
		if out := os.Getenv("GODOCS_PICK_OUT"); out != "" {
			return os.WriteFile(out, []byte(res.Pkg+"\t"+res.Anchor+"\n"), 0o644)
		}
		return page(func(w io.Writer) error {
			return render(w, res.Pkg, res.Anchor, formatFor(opts, true))
		})

	case "_rows":
		return pickRows(strings.Join(args, " "), opts.stdOnly)

	case "search":
		files, err := indexFiles(opts.stdOnly)
		if err != nil {
			return err
		}
		limit := opts.limit
		if limit == 0 {
			limit = 50
		}
		entries, err := searchIndex(files, strings.Join(args, " "), opts.kind, limit, opts.namesOnly)
		if err != nil {
			return err
		}
		for _, e := range entries {
			fmt.Println(strings.Join(e.fields, "\t"))
		}
		return nil

	case "doc", "show":
		if len(args) == 0 {
			return errors.New("doc needs a target, e.g. net/http.Client.Do")
		}
		pkg, symbol, err := resolveTarget(args[0], opts.stdOnly)
		if err != nil {
			return err
		}
		return page(func(w io.Writer) error {
			return render(w, pkg, symbol, formatFor(opts, true))
		})

	case "render":
		if len(args) == 0 {
			return errors.New("render needs a package")
		}
		symbol := ""
		if len(args) > 1 {
			symbol = args[1]
		}
		return render(os.Stdout, args[0], symbol, formatFor(opts, false))

	case "buffer":
		return bufferCmd(opts, args)

	case "popup":
		if len(args) == 0 {
			args = []string{"pick"}
		}
		return popup(args)

	case "url", "open":
		if len(args) == 0 {
			return errors.New(cmd + " needs a target")
		}
		pkg, symbol, err := resolveTarget(args[0], opts.stdOnly)
		if err != nil {
			return err
		}
		if cmd == "url" {
			fmt.Println(pkgsiteURL(pkg, symbol))
			return nil
		}
		return openURL(pkgsiteURL(pkg, symbol))

	case "open-url":
		// Internal: package and anchor already split, used by the fzf binding.
		if len(args) == 0 {
			return errors.New("open-url needs a package")
		}
		anchor := ""
		if len(args) > 1 {
			anchor = args[1]
		}
		return openURL(pkgsiteURL(args[0], anchor))

	case "resolve":
		if len(args) == 0 {
			return errors.New("resolve needs a target")
		}
		pkg, symbol, err := resolveTarget(args[0], opts.stdOnly)
		if err != nil {
			return err
		}
		fmt.Printf("%s\t%s\n", pkg, symbol)
		return nil

	case "clean":
		fmt.Println(cleanTarget(strings.Join(args, " ")))
		return nil

	case "index":
		p, err := buildStdIndex(opts.force)
		if err != nil {
			return err
		}
		fmt.Println(p)
		return nil

	case "deps":
		p, err := buildDepsIndex(opts.force)
		if err != nil {
			return err
		}
		fmt.Println(p)
		return nil

	case "where":
		fmt.Printf("cache:   %s\n", cacheDir())
		fmt.Printf("stdlib:  %s\n", stdIndexPath())
		if p, ok := depsIndexPath(); ok {
			fmt.Printf("deps:    %s\n", p)
		}
		fmt.Printf("buffers: %s\n", bufferDir())
		return nil
	}

	return fmt.Errorf("unknown command %q", cmd)
}

// parseOptions pulls shared flags out from wherever they appear, so that both
// `godocs doc x --format md` and `godocs --format md doc x` work.
func parseOptions(argv []string) (options, []string) {
	opts := options{}
	var args []string
	for i := 0; i < len(argv); i++ {
		next := func() string {
			if i+1 < len(argv) {
				i++
				return argv[i]
			}
			return ""
		}
		switch argv[i] {
		case "--std":
			opts.stdOnly = true
		case "--force":
			opts.force = true
		case "--pick":
			opts.pick = true
		case "--names-only":
			opts.namesOnly = true
		case "--md":
			opts.format = "md"
		case "--text":
			opts.format = "text"
		case "--kind":
			opts.kind = next()
		case "--format":
			opts.format = next()
		case "--fallback":
			opts.fallback = next()
		case "--limit":
			fmt.Sscanf(next(), "%d", &opts.limit)
		default:
			args = append(args, argv[i])
		}
	}
	return opts, args
}

// formatFor defaults to wrapped plain text when a human is reading it directly.
func formatFor(opts options, interactive bool) string {
	if opts.format != "" {
		return opts.format
	}
	if interactive {
		return "text"
	}
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		return "text"
	}
	return "md"
}

// bufferCmd renders documentation to a file and prints its path, for editors
// that can open the output of a synchronous command.
//
// Anything that does not resolve to exactly one symbol opens the picker seeded
// with it, rather than rendering a list of near misses that cannot be acted on.
func bufferCmd(opts options, args []string) error {
	cancel := func() error {
		if opts.fallback != "" {
			if fi, err := os.Stat(opts.fallback); err == nil && !fi.IsDir() {
				fmt.Println(opts.fallback)
			}
		}
		return nil
	}

	target := cleanTarget(strings.Join(args, " "))

	if !opts.pick && target != "" {
		if pkg, symbol, err := resolveTarget(target, opts.stdOnly); err == nil {
			out, err := renderToBuffer(pkg, symbol)
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		}
	}

	out, err := pickToBuffer(target, opts.stdOnly)
	if err != nil {
		return cancel()
	}
	fmt.Println(out)
	return nil
}

// page sends long output through a pager when a human is watching. Rendered
// documentation is a few kilobytes at most, so buffering it is simpler than
// plumbing a second pipe.
func page(write func(io.Writer) error) error {
	var buf bytes.Buffer
	if err := write(&buf); err != nil {
		return err
	}

	fi, err := os.Stdout.Stat()
	interactive := err == nil && fi.Mode()&os.ModeCharDevice != 0
	pager, lookErr := exec.LookPath(envOr("PAGER", "less"))
	if !interactive || lookErr != nil {
		_, err := os.Stdout.Write(buf.Bytes())
		return err
	}

	cmd := exec.Command(pager, "-R")
	cmd.Stdin = &buf
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
