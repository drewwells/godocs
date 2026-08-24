package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// goPrinter renders declarations with spaces so struct field alignment
// survives being pasted into a Markdown code block.
var goPrinter = &printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 4}

type listPkg struct {
	Dir        string
	ImportPath string
	Name       string
	Doc        string
	GoFiles    []string
	Standard   bool
	Error      *struct{ Err string }
}

type row struct {
	kind, display, target, pkg, anchor, sig, synopsis string
}

type pkgFilter int

const (
	allPkgs pkgFilter = iota
	stdOnly
	nonStdOnly
)

func (f pkgFilter) keep(p listPkg) bool {
	switch f {
	case stdOnly:
		return p.Standard
	case nonStdOnly:
		return !p.Standard
	}
	return true
}

// buildIndex walks the packages matched by patterns and writes one TSV row per
// package and per exported symbol.
//
// Columns: kind, display, target, pkg, anchor, signature, synopsis
//
//	kind      pkg | func | type | method | const | var
//	display   what a human searches for: "net/http", "http.Client.Do"
//	target    argument for `go doc`:     "net/http", "net/http.Client.Do"
//	anchor    pkg.go.dev fragment:       "", "Client.Do"
func buildIndex(w io.Writer, patterns []string, filter pkgFilter) error {
	pkgs, err := listPackages(patterns)
	if err != nil {
		return err
	}

	var rows []row
	seen := map[string]bool{}
	for _, p := range pkgs {
		if p.Error != nil || p.Dir == "" || len(p.GoFiles) == 0 {
			continue
		}
		if !filter.keep(p) {
			continue
		}
		if isInternal(p.ImportPath) || isVendored(p.ImportPath) {
			continue
		}
		for _, r := range indexPackage(p) {
			key := r.kind + "\x00" + r.target
			if seen[key] {
				continue
			}
			seen[key] = true
			rows = append(rows, r)
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].pkg != rows[j].pkg {
			return rows[i].pkg < rows[j].pkg
		}
		return rows[i].display < rows[j].display
	})

	bw := bufio.NewWriter(w)
	for _, r := range rows {
		fmt.Fprintf(bw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.kind, r.display, r.target, r.pkg, r.anchor, r.sig, r.synopsis)
	}
	return bw.Flush()
}

func isInternal(importPath string) bool {
	return strings.Contains(importPath, "internal/") ||
		strings.HasSuffix(importPath, "/internal") ||
		importPath == "internal"
}

func isVendored(importPath string) bool {
	return strings.HasPrefix(importPath, "vendor/") || strings.Contains(importPath, "/vendor/")
}

func listPackages(patterns []string) ([]listPkg, error) {
	cmdArgs := append([]string{"list", "-e",
		"-json=Dir,ImportPath,Name,Doc,GoFiles,Standard,Error"}, patterns...)
	cmd, err := goCommand(cmdArgs...)
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var pkgs []listPkg
	dec := json.NewDecoder(out)
	for {
		var p listPkg
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	// `go list -e` reports per-package errors in the JSON; a non-zero exit here
	// usually just means some pattern had no match, so don't treat it as fatal.
	_ = cmd.Wait()
	return pkgs, nil
}

func indexPackage(p listPkg) []row {
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range p.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.ParseComments)
		if err != nil {
			continue
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil
	}

	dp, err := doc.NewFromFiles(fset, files, p.ImportPath)
	if err != nil {
		return nil
	}

	short := p.Name
	if short == "" {
		short = path(p.ImportPath)
	}

	rows := []row{{
		kind:     "pkg",
		display:  p.ImportPath,
		target:   p.ImportPath,
		pkg:      p.ImportPath,
		sig:      "package " + short,
		synopsis: oneline(dp.Synopsis(dp.Doc)),
	}}

	add := func(kind, name, sig, docText string) {
		rows = append(rows, row{
			kind:     kind,
			display:  short + "." + name,
			target:   p.ImportPath + "." + name,
			pkg:      p.ImportPath,
			anchor:   name,
			sig:      oneline(sig),
			synopsis: oneline(dp.Synopsis(docText)),
		})
	}

	for _, f := range dp.Funcs {
		add("func", f.Name, funcSig(fset, f.Decl), f.Doc)
	}
	for _, v := range dp.Consts {
		for _, n := range v.Names {
			add("const", n, "const "+n, v.Doc)
		}
	}
	for _, v := range dp.Vars {
		for _, n := range v.Names {
			add("var", n, "var "+n, v.Doc)
		}
	}
	for _, t := range dp.Types {
		add("type", t.Name, typeSig(fset, t), t.Doc)
		for _, f := range t.Funcs {
			add("func", f.Name, funcSig(fset, f.Decl), f.Doc)
		}
		for _, m := range t.Methods {
			add("method", t.Name+"."+m.Name, funcSig(fset, m.Decl), m.Doc)
		}
		for _, v := range t.Consts {
			for _, n := range v.Names {
				add("const", n, "const "+n, v.Doc)
			}
		}
		for _, v := range t.Vars {
			for _, n := range v.Names {
				add("var", n, "var "+n, v.Doc)
			}
		}
	}
	return rows
}

// funcSig renders a func declaration without its body or doc comment.
func funcSig(fset *token.FileSet, fn *ast.FuncDecl) string {
	if fn == nil {
		return ""
	}
	body, docc := fn.Body, fn.Doc
	fn.Body, fn.Doc = nil, nil
	defer func() { fn.Body, fn.Doc = body, docc }()

	var b strings.Builder
	if err := goPrinter.Fprint(&b, fset, fn); err != nil {
		return "func " + fn.Name.Name
	}
	return b.String()
}

// typeSig renders a type's kind without expanding struct or interface bodies,
// which would swamp a one-line index.
func typeSig(fset *token.FileSet, t *doc.Type) string {
	if t.Decl == nil || len(t.Decl.Specs) == 0 {
		return "type " + t.Name
	}
	spec, ok := t.Decl.Specs[0].(*ast.TypeSpec)
	if !ok || spec.Type == nil {
		return "type " + t.Name
	}
	switch spec.Type.(type) {
	case *ast.StructType:
		return "type " + t.Name + " struct"
	case *ast.InterfaceType:
		return "type " + t.Name + " interface"
	}
	var b strings.Builder
	if err := goPrinter.Fprint(&b, fset, spec.Type); err != nil {
		return "type " + t.Name
	}
	return "type " + t.Name + " " + b.String()
}

func path(importPath string) string {
	if i := strings.LastIndex(importPath, "/"); i >= 0 {
		return importPath[i+1:]
	}
	return importPath
}

func oneline(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
