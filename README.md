# godocs

Fast, offline, fuzzy lookup of Go documentation — from your terminal, your
editor, or Raycast.

`go doc` already has the content. What it lacks is a way to find things: you
have to know the import path and the exact symbol name before it will tell you
anything. `godocs` indexes every package and exported symbol up front, so you
can type `marshal` and get `json.Marshal`, or `wtgrp` and get
`sync.WaitGroup`.

Nothing talks to the network unless you ask it to open a browser.

![The godocs picker open over Helix in a tmux popup. The query "cond" has
matched sync.Cond and its methods, each row showing a signature and the first
line of its documentation; the pane below previews the full docs for the
selected sync.Cond.](helix-usage.png)

*The picker, here over Helix. Results rank as you type and the pane below
previews the selected symbol; `enter` opens it as a Markdown buffer.*

## Install

```sh
GOBIN="$HOME/.local/bin" go install github.com/drewwells/godocs@latest
```

`fzf` is needed for the interactive picker, and `tmux` for the editor popups.
Neither is required for `godocs doc`, `search` or `url`.

> **On GOBIN.** If you manage Go with a version manager, the default `GOBIN` may
> live inside the toolchain directory itself — which is replaced on every Go
> upgrade, silently taking installed binaries with it. Pin it somewhere stable.

## Use

```
godocs                      browse everything in the picker
godocs marshal              picker, seeded with a query
godocs doc http.Client.Do   read one symbol
godocs url time.Duration    print its pkg.go.dev URL
godocs open sync.WaitGroup  open that in a browser
```

In the picker: `enter` reads the docs, `ctrl-o` opens pkg.go.dev, `ctrl-y`
copies the import path, `ctrl-/` cycles the preview layout.

Targets resolve in either spelling — `net/http.Client.Do` as `go doc` wants it,
or `http.Client.Do` as your source actually reads.

## How it works

The index is a flat TSV under `~/.cache/godocs/`: one row per package and per
exported symbol, carrying the signature and the first sentence of the doc
comment.

```
func	json.Marshal	encoding/json.Marshal	encoding/json	Marshal	func Marshal(v any) ([]byte, error)	Marshal returns the JSON encoding of v.
```

Building it walks the packages `go list` reports and reads their ASTs with
`go/doc`. The standard library takes about a second and yields ~11,000 rows.
Searching that is ~8ms, which is what lets the picker re-rank on every
keystroke instead of handing filtering off to fzf.

Ranking is name-first — exact match, then symbol prefix, then substring, then
subsequence — with synopsis text as a last resort. Everyday packages break
ties, so `marshal` puts `json.Marshal` above `asn1.Marshal`.

Documentation is rendered through `go/doc/comment`, so doc links become real
pkg.go.dev URLs and code blocks stay code blocks. `--format text` wraps for a
terminal; `--format md` is Markdown.

## Your own dependencies

The standard library is indexed automatically. A module's dependencies mean
resolving its build graph, so that is opt-in — once per project:

```sh
cd ~/src/some-project
godocs deps
```

From then on, any `godocs` run from inside that module searches the standard
library *and* those dependencies, and `godocs doc errgroup.Group.Go` resolves.
The index refreshes itself when `go.mod` changes.

## Editors

`godocs buffer` renders documentation to a Markdown file and prints its path,
for editors that can open the output of a synchronous command. Reading docs in a
real buffer beats a pager: search, yank and buffer history all behave normally.

Given text it cannot resolve to exactly one symbol, it opens the picker seeded
with that text, rather than rendering a list of near misses you cannot act on.

### Helix

**Requirements**

- Helix **25.07 or newer** — `%{...}` and `%sh{...}` expansions were introduced
  in [25.07](https://helix-editor.com/news/release-25-07-highlights/) and these
  bindings do not work without them. Check with `hx --version`.
- `tmux`, for the picker popup. Without it the direct lookup still works, but
  anything that would fall back to the picker quietly does nothing.
- `fzf`, for the picker itself.
- `godocs` on the `PATH` **Helix itself sees** — see troubleshooting below.

**Configuration**

The picker at the top of this README is `+d` in action. Add this to
`~/.config/helix/config.toml`:

```toml
[keys.normal."+"]
# +d — search in a popup; whatever you pick opens as a Markdown buffer
d = ":open %sh{godocs buffer --fallback \"%{buffer_name}\" --pick}"
# +D — look up the word under the cursor: straight to the docs if it resolves,
#      otherwise the picker, seeded with it
D = [
  "move_prev_long_word_start",
  "move_next_long_word_end",
  ":open %sh{godocs buffer --fallback \"%{buffer_name}\" \"%{selection}\"}",
  "collapse_selection",
]

[keys.select."+"]
# +d — look up the current selection
d = ":open %sh{godocs buffer --fallback \"%{buffer_name}\" \"%{selection}\"}"
```

`+` is not a Helix default — these lines create it as a new prefix key. If you
would rather hang them off the space menu, use `[keys.normal.space.d]` and so
on; just avoid keys Helix already uses (`space d` is the diagnostics picker).

**Why it is written this way**

`%sh{...}` runs **synchronously**: it blocks until the popup closes, then hands
the resulting path to `:open`. The asynchronous `:sh` would race, opening the
file before it had been written.

`--fallback "%{buffer_name}"` re-opens the buffer you are already on if you
cancel the picker. Without it, `:open` is handed an empty path and complains.

The long-word motions on `+D` deliberately grab the whole token —
`http.Client.Do(req)` — and `godocs` trims the call syntax off before resolving.
A sloppy grab is not a dead end: it just becomes the picker's starting query.

**Troubleshooting**

*Nothing happens, or `'open': path must be a regular file`* — check that Helix
can see the binary. `%sh{}` runs through `sh -c` with Helix's own environment,
which is not your interactive shell's. Test it from inside Helix:

```
:echo %sh{command -v godocs}
```

Empty means `godocs` is not on that `PATH`. Either launch Helix from a shell
that has it, or spell out the path in the bindings — `%sh{...}` runs through a
shell, so `~/.local/bin/godocs buffer ...` and `$HOME/...` both expand.

*The popup never appears, and the statusline says `no current client`* — a
tmux quirk rather than a godocs one, and `godocs popup` already handles it: an
editor's `:sh` runs detached from any tmux client, so `display-popup` has no
client to draw on unless it is given a target pane. If you are wrapping godocs
in your own script, pass `-t "$TMUX_PANE"`.

*Isolating the problem* — run the same command from a shell first:

```sh
godocs buffer http.Client.Do    # should print a path to a .md file
```

If that works but the keybinding does not, the problem is the binding or the
environment, not godocs.

## Raycast

`raycast/` is a Raycast extension that drives the same binary, so it returns the
same results in the same order.

![The Go Docs command in Raycast. A search for "sum256" lists sha256.Sum256,
sha3.Sum256, sha3.SumSHAKE256 and sha512.Sum512_256; the detail pane shows the
signature, synopsis and import path for the selected sha256.Sum256, with "Open
on Pkg.go.dev" bound to enter.](raycast.png)

```sh
cd raycast
npm install
npx ray develop      # registers it with Raycast
```

### How it finds the binary

1. the **godocs Path** preference, if you set one
2. `godocs` on PATH — where `go install` puts it
3. otherwise it downloads the pinned release from GitHub, once

The download is verified against a SHA-256 recorded in
[`raycast/src/release.ts`](raycast/src/release.ts) — in reviewable source, not
fetched alongside the binary, because a checksum served from the same place as
the download proves nothing. Bytes are verified *before* being decompressed or
written anywhere executable, and a mismatch leaves nothing on disk. The
extension never compiles anything on your machine.

Releases are built by [a GitHub Actions workflow](.github/workflows/release.yml)
that publishes build provenance attestations, so a published binary can be
traced back to the commit and workflow that produced it:

```sh
gh attestation verify godocs_<version>_darwin_arm64.gz --repo drewwells/godocs
```

`npm test` exercises that whole path — install, tamper-rejection, preference
precedence — against locally built artifacts.

Set an alias of `go` on the **Search Go Docs** command to look things up by
typing `go {term}`.

Results render the documentation in a side pane. `enter` opens pkg.go.dev by
default (configurable), `cmd+d` toggles the preview, and there are copy actions
for the import path, the symbol, the import statement and the equivalent
`go doc` command. Point its **Project Directory** preference at a Go module to
search that module's dependencies too.

## Cache

`godocs where` prints the paths. The standard library index is keyed by Go
version, so upgrading Go rebuilds it; `godocs index --force` rebuilds by hand.
Deleting `~/.cache/godocs` is always safe.

## License

MIT
