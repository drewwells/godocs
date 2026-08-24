package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Everyday packages win ties against their obscure namesakes, so that
// "marshal" surfaces json.Marshal ahead of asn1.Marshal.
var corePkgs = strings.Fields(`
	fmt strings strconv os io bytes errors context time sync encoding/json
	net/http sort slices maps bufio path/filepath regexp log math cmp
`)

var commonPkgs = strings.Fields(`
	sync/atomic net/url path log/slog testing math/rand flag io/fs
	encoding/base64 encoding/csv encoding/xml text/template html/template
	os/exec net database/sql unicode/utf8 reflect crypto/rand embed iter
	os/signal net/netip mime/multipart compress/gzip archive/tar archive/zip
	hash/fnv crypto/sha256 encoding/hex encoding/binary runtime runtime/debug
`)

var pkgTier = func() map[string]int {
	m := make(map[string]int, len(corePkgs)+len(commonPkgs))
	for _, p := range commonPkgs {
		m[p] = 1
	}
	for _, p := range corePkgs {
		m[p] = 0
	}
	return m
}()

func tierOf(pkg string) int {
	if t, ok := pkgTier[pkg]; ok {
		return t
	}
	return 2
}

var kindWeight = map[string]int{
	"pkg": 0, "func": 1, "type": 2, "method": 3, "const": 4, "var": 5,
}

// entry mirrors one line of the index TSV.
type entry struct {
	fields              []string
	kind, pkg           string
	lowDisplay, lowBase string
	lowTail, lowSig     string
	lowSynopsis         string
}

func parseEntry(line string) (entry, bool) {
	f := strings.Split(line, "\t")
	if len(f) < 7 {
		return entry{}, false
	}
	d := strings.ToLower(f[1])
	return entry{
		fields:      f,
		kind:        f[0],
		pkg:         f[3],
		lowDisplay:  d,
		lowBase:     afterFirst(d, '.'),
		lowTail:     afterLastAny(d, "./"),
		lowSig:      strings.ToLower(f[5]),
		lowSynopsis: strings.ToLower(f[6]),
	}, true
}

func afterFirst(s string, sep byte) string {
	if i := strings.IndexByte(s, sep); i >= 0 {
		return s[i+1:]
	}
	return s
}

func afterLastAny(s, seps string) string {
	if i := strings.LastIndexAny(s, seps); i >= 0 {
		return s[i+1:]
	}
	return s
}

// subseq reports whether needle appears in hay as an ordered subsequence,
// which is what makes "wtgrp" find sync.WaitGroup.
func subseq(hay, needle string) bool {
	if needle == "" {
		return true
	}
	j := 0
	for i := 0; i < len(hay) && j < len(needle); i++ {
		if hay[i] == needle[j] {
			j++
		}
	}
	return j == len(needle)
}

// rankName scores how well q matches an entry's name. Lower is better;
// -1 means no match at all.
func (e entry) rankName(q string) int {
	switch {
	case e.lowDisplay == q:
		return 0
	case e.lowBase == q || e.lowTail == q:
		return 1
	case strings.HasPrefix(e.lowBase, q):
		return 2
	case strings.HasPrefix(e.lowDisplay, q):
		return 3
	case strings.HasPrefix(e.lowTail, q):
		return 4
	case strings.Contains(e.lowDisplay, q):
		return 5
	case subseq(e.lowBase, q):
		return 7
	case subseq(e.lowDisplay, q):
		return 8
	}
	return -1
}

type scored struct {
	score int
	entry entry
}

// searchIndex ranks the rows of the given index files against a query. An
// empty query returns everything, ordered by package tier, which makes a
// reasonable browse list.
func searchIndex(files []string, query, kind string, limit int, namesOnly bool) ([]entry, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	tokens := strings.Fields(q)

	var out []scored
	err := eachIndexLine(files, func(line string) {
		e, ok := parseEntry(line)
		if !ok {
			return
		}
		if kind != "" && e.kind != kind {
			return
		}

		var s int
		if len(tokens) == 0 {
			s = 6
		} else {
			// Every token has to appear somewhere; the first one ranks.
			if len(tokens) > 1 {
				hay := e.lowDisplay + " " + e.lowSig + " " + e.lowSynopsis
				for _, t := range tokens[1:] {
					if !strings.Contains(hay, t) {
						return
					}
				}
			}
			s = e.rankName(tokens[0])
			if s < 0 {
				if namesOnly || !strings.Contains(e.lowSynopsis, tokens[0]) {
					return
				}
				s = 10
			}
			if len(tokens) > 1 && s > 5 {
				s = 6
			}
		}

		n := len(e.lowDisplay)
		if n > 99 {
			n = 99
		}
		out = append(out, scored{
			score: s*10000 + tierOf(e.pkg)*1000 + kindWeight[e.kind]*100 + n,
			entry: e,
		})
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].score < out[j].score })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	entries := make([]entry, len(out))
	for i, s := range out {
		entries[i] = s.entry
	}
	return entries, nil
}

// resolveTarget splits a target into its package and symbol. It accepts both
// the `go doc` spelling ("net/http.Client.Do") and the short spelling a Go file
// actually contains ("http.Client.Do"), because the latter is what you select
// in an editor.
func resolveTarget(target string, stdLibOnly bool) (pkg, symbol string, err error) {
	t := strings.TrimSpace(target)
	if t == "" {
		return "", "", errors.New("empty target")
	}
	files, err := indexFiles(stdLibOnly)
	if err != nil {
		return "", "", err
	}
	lowerT := strings.ToLower(t)

	var bestPkg string
	var exactHit, foldedHit []string
	err = eachIndexLine(files, func(line string) {
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			return
		}
		if f[0] == "pkg" && (t == f[1] || strings.HasPrefix(t, f[1]+".")) {
			if len(f[1]) > len(bestPkg) {
				bestPkg = f[1]
			}
		}
		if exactHit == nil && f[1] == t {
			exactHit = f
		}
		if foldedHit == nil && strings.ToLower(f[1]) == lowerT {
			foldedHit = f
		}
	})
	if err != nil {
		return "", "", err
	}

	// An import-path prefix is unambiguous, so it wins over a short-name match.
	if bestPkg != "" {
		return bestPkg, strings.TrimPrefix(strings.TrimPrefix(t, bestPkg), "."), nil
	}
	if hit := exactHit; hit != nil {
		return hit[3], hit[4], nil
	}
	if hit := foldedHit; hit != nil {
		return hit[3], hit[4], nil
	}
	return "", "", fmt.Errorf("cannot resolve %q", target)
}

// cleanTarget trims text grabbed from an editor buffer back to something
// resolvable: *http.Client.Do(req), `json.Marshal` and "time.Duration," all
// become the bare qualified identifier.
func cleanTarget(text string) string {
	t := text
	if i := strings.IndexAny(t, "\n"); i >= 0 {
		t = t[:i]
	}
	for _, cut := range []string{"(", "[", ",", "{"} {
		if i := strings.Index(t, cut); i >= 0 {
			t = t[:i]
		}
	}
	keep := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || r == '_' || r == '/'
	}
	t = strings.TrimLeftFunc(t, func(r rune) bool { return !keep(r) })
	t = strings.TrimRightFunc(t, func(r rune) bool { return !keep(r) && r != '.' })
	return strings.TrimRight(t, ".")
}

func eachIndexLine(paths []string, fn func(string)) error {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			fn(sc.Text())
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
