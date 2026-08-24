package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// render writes documentation for an import path, optionally narrowed to a
// symbol ("Client") or method ("Client.Do"), as Markdown or wrapped plain text.
//
// It takes the package and symbol as separate arguments rather than parsing a
// `go doc`-style target, because callers get both straight out of the index.
func render(w io.Writer, importPath, symbol, format string) error {
	pkgs, err := listPackages([]string{importPath})
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("cannot resolve package %q", importPath)
	}
	p := pkgs[0]
	if p.Error != nil {
		return fmt.Errorf("%s: %s", importPath, p.Error.Err)
	}
	if p.Dir == "" || len(p.GoFiles) == 0 {
		return fmt.Errorf("no Go files for %s on this platform", importPath)
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range p.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.ParseComments)
		if err != nil {
			continue
		}
		files = append(files, f)
	}
	dp, err := doc.NewFromFiles(fset, files, p.ImportPath)
	if err != nil {
		return fmt.Errorf("%s: %w", importPath, err)
	}

	out := bufio.NewWriter(w)
	r := &renderer{w: out, fset: fset, dp: dp, format: format}

	if symbol == "" {
		r.pkg(p)
	} else if !r.symbol(p, symbol) {
		return fmt.Errorf("%s has no symbol %q", importPath, symbol)
	}
	return out.Flush()
}

type renderer struct {
	w      *bufio.Writer
	fset   *token.FileSet
	dp     *doc.Package
	format string
}

func (r *renderer) markdown() bool { return r.format != "text" }

// prose converts a Go doc comment into Markdown or wrapped plain text.
func (r *renderer) prose(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	// Use the package's own parser/printer so unqualified doc links such as
	// [Response] resolve against this package instead of being escaped.
	parsed := r.dp.Parser().Parse(text)
	pr := r.dp.Printer()
	pr.DocLinkBaseURL = "https://pkg.go.dev"
	if r.markdown() {
		md := string(pr.Markdown(parsed))
		// Same-package doc links print as bare "#Name" anchors; make them
		// absolute so they are clickable outside of pkg.go.dev itself.
		return strings.ReplaceAll(md, "](#", "](https://pkg.go.dev/"+r.dp.ImportPath+"#")
	}
	pr.TextWidth = 78
	return string(pr.Text(parsed))
}

func (r *renderer) heading(s string) {
	if r.markdown() {
		fmt.Fprintf(r.w, "# %s\n\n", s)
	} else {
		fmt.Fprintf(r.w, "%s\n%s\n\n", s, strings.Repeat("=", len(s)))
	}
}

func (r *renderer) code(s string) {
	if s == "" {
		return
	}
	if r.markdown() {
		fmt.Fprintf(r.w, "```go\n%s\n```\n\n", s)
		return
	}
	for _, line := range strings.Split(s, "\n") {
		fmt.Fprintf(r.w, "    %s\n", line)
	}
	r.w.WriteString("\n")
}

func (r *renderer) text(s string) {
	if s = strings.TrimRight(s, "\n"); s != "" {
		fmt.Fprintf(r.w, "%s\n\n", s)
	}
}

func (r *renderer) pkg(p listPkg) {
	r.heading(p.ImportPath)
	r.code(fmt.Sprintf("import %q", p.ImportPath))
	r.text(r.prose(r.dp.Doc))

	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		sort.Strings(lines)
		if r.markdown() {
			fmt.Fprintf(r.w, "## %s\n\n```go\n%s\n```\n\n", title, strings.Join(lines, "\n"))
		} else {
			fmt.Fprintf(r.w, "%s\n%s\n\n", title, strings.Repeat("-", len(title)))
			for _, l := range lines {
				fmt.Fprintf(r.w, "    %s\n", l)
			}
			r.w.WriteString("\n")
		}
	}

	var funcs, types, values []string
	for _, f := range r.dp.Funcs {
		funcs = append(funcs, funcSig(r.fset, f.Decl))
	}
	for _, t := range r.dp.Types {
		types = append(types, typeSig(r.fset, t))
		for _, f := range t.Funcs {
			funcs = append(funcs, funcSig(r.fset, f.Decl))
		}
	}
	for _, v := range r.dp.Consts {
		values = append(values, "const "+strings.Join(v.Names, ", "))
	}
	for _, v := range r.dp.Vars {
		values = append(values, "var "+strings.Join(v.Names, ", "))
	}

	section("Types", types)
	section("Functions", funcs)
	section("Constants and Variables", values)
}

func (r *renderer) symbol(p listPkg, symbol string) bool {
	name, method, isMethod := strings.Cut(symbol, ".")

	title := p.Name
	if title == "" {
		title = path(p.ImportPath)
	}
	title += "." + symbol

	emit := func(sig, docText string) {
		r.heading(title)
		r.code(sig)
		r.text(r.prose(docText))
		if r.markdown() {
			fmt.Fprintf(r.w, "---\n\n`import %q`\n", p.ImportPath)
		} else {
			fmt.Fprintf(r.w, "import %q\n", p.ImportPath)
		}
	}

	if !isMethod {
		for _, f := range r.dp.Funcs {
			if f.Name == name {
				emit(funcSig(r.fset, f.Decl), f.Doc)
				return true
			}
		}
		for _, v := range r.dp.Consts {
			if hasName(v.Names, name) {
				emit(r.valueSig(v), v.Doc)
				return true
			}
		}
		for _, v := range r.dp.Vars {
			if hasName(v.Names, name) {
				emit(r.valueSig(v), v.Doc)
				return true
			}
		}
	}

	for _, t := range r.dp.Types {
		if t.Name != name {
			continue
		}
		if isMethod {
			for _, m := range t.Methods {
				if m.Name == method {
					emit(funcSig(r.fset, m.Decl), m.Doc)
					return true
				}
			}
			// A constructor such as http.NewRequest is attached to its result
			// type by go/doc, so Type.Func is a valid "Type.Name" target too.
			for _, f := range t.Funcs {
				if f.Name == method {
					emit(funcSig(r.fset, f.Decl), f.Doc)
					return true
				}
			}
			return false
		}
		r.typeDetail(p, t)
		return true
	}

	// Constructors are indexed under their own name (http.NewRequest), so fall
	// back to scanning every type's associated funcs.
	if !isMethod {
		for _, t := range r.dp.Types {
			for _, f := range t.Funcs {
				if f.Name == name {
					emit(funcSig(r.fset, f.Decl), f.Doc)
					return true
				}
			}
			for _, v := range t.Consts {
				if hasName(v.Names, name) {
					emit(r.valueSig(v), v.Doc)
					return true
				}
			}
			for _, v := range t.Vars {
				if hasName(v.Names, name) {
					emit(r.valueSig(v), v.Doc)
					return true
				}
			}
		}
	}
	return false
}

// typeDetail prints the full declaration of a type plus its constructors and
// method set, which is what you almost always want when looking a type up.
func (r *renderer) typeDetail(p listPkg, t *doc.Type) {
	short := p.Name
	if short == "" {
		short = path(p.ImportPath)
	}
	r.heading(short + "." + t.Name)

	decl := t.Decl
	saved := decl.Doc
	decl.Doc = nil
	var b strings.Builder
	_ = goPrinter.Fprint(&b, r.fset, decl)
	decl.Doc = saved
	r.code(b.String())

	r.text(r.prose(t.Doc))

	var ctors, methods []string
	for _, f := range t.Funcs {
		ctors = append(ctors, funcSig(r.fset, f.Decl))
	}
	for _, m := range t.Methods {
		methods = append(methods, funcSig(r.fset, m.Decl))
	}
	sort.Strings(ctors)
	sort.Strings(methods)

	block := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		if r.markdown() {
			fmt.Fprintf(r.w, "## %s\n\n```go\n%s\n```\n\n", title, strings.Join(lines, "\n"))
		} else {
			fmt.Fprintf(r.w, "%s\n%s\n\n", title, strings.Repeat("-", len(title)))
			for _, l := range lines {
				fmt.Fprintf(r.w, "    %s\n", l)
			}
			r.w.WriteString("\n")
		}
	}
	block("Constructors", ctors)
	block("Methods", methods)

	if r.markdown() {
		fmt.Fprintf(r.w, "---\n\n`import %q`\n", p.ImportPath)
	} else {
		fmt.Fprintf(r.w, "import %q\n", p.ImportPath)
	}
}

func (r *renderer) valueSig(v *doc.Value) string {
	saved := v.Decl.Doc
	v.Decl.Doc = nil
	var b strings.Builder
	_ = goPrinter.Fprint(&b, r.fset, v.Decl)
	v.Decl.Doc = saved
	return b.String()
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
